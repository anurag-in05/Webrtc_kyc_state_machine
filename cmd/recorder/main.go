package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"google.golang.org/grpc"

	"kyc-monorepo/internal/recorder"
	recorderpb "kyc-monorepo/proto"
)

func main() {
	grpcAddr := getenv("GRPC_ADDR", ":9091")
	httpAddr := getenv("HTTP_ADDR", ":9090")
	dir := getenv("RECORDINGS_DIR", "./recordings")

	// S3 upload at finalize. Nil when AWS_S3_BUCKET is unset → local URLs.
	s3 := recorder.NewS3Uploader(
		os.Getenv("AWS_S3_BUCKET"),
		os.Getenv("AWS_MEDIA_FOLDER"),
		os.Getenv("AWS_REGION"),
		os.Getenv("AWS_URL"),
	)

	// HTTP control plane: /finalize + /status. Runs in its own goroutine.
	api := recorder.NewHTTPAPI(dir, s3)
	go func() {
		log.Printf("recorder: HTTP on %s", httpAddr)
		if err := http.ListenAndServe(httpAddr, api.Handler()); err != nil {
			log.Fatalf("http serve: %v", err)
		}
	}()

	// gRPC media plane: the frame ingest stream.
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", grpcAddr, err)
	}
	srv := grpc.NewServer()
	recorderpb.RegisterRecorderServer(srv, recorder.NewService(dir))

	log.Printf("recorder: gRPC on %s, recordings -> %s", grpcAddr, dir)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
