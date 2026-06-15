package main

import (
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	"kyc-monorepo/internal/recorder"
	recorderpb "kyc-monorepo/proto"
)

func main() {
	grpcAddr := getenv("GRPC_ADDR", ":9091")
	dir := getenv("RECORDINGS_DIR", "./recordings")

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
