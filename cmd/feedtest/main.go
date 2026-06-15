package main
import (
	"context"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	recorderpb "kyc-monorepo/proto"
)

func main() {
	conn, err := grpc.NewClient("localhost:9091",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	client := recorderpb.NewRecorderClient(conn)
	stream, err := client.RecordStream(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	const sid = "test-session-001"
	// 5 turns of fake audio: 1920 bytes = 20ms of 48kHz mono s16le.
	for i := 0; i < 5; i++ {
		send(stream, &recorderpb.Frame{SessionId: sid, Kind: recorderpb.Kind_USER_PCM, TsUs: uint64(i) * 20000, Data: make([]byte, 1920)})
		send(stream, &recorderpb.Frame{SessionId: sid, Kind: recorderpb.Kind_AGENT_PCM, TsUs: uint64(i) * 20000, Data: make([]byte, 1920)})
	}
	// one fake video AU — just exercises the VIDEO_AU route; the writer is still a stub.
	send(stream, &recorderpb.Frame{SessionId: sid, Kind: recorderpb.Kind_VIDEO_AU, TsUs: 0, Data: []byte{0, 0, 0, 1, 0x65}})

	ack, err := stream.CloseAndRecv()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("recorder acked %d frames", ack.Frames)
}

func send(s recorderpb.Recorder_RecordStreamClient, f *recorderpb.Frame) {
	if err := s.Send(f); err != nil {
		log.Fatal(err)
	}
}