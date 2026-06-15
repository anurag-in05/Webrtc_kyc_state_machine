package recorder

import (
	"io"
	"log"
	recorderpb "kyc-monorepo/proto"
)

type service struct {
	recorderpb.UnimplementedRecorderServer // forward-compat: new RPCs won't break the build
	recordingsDir string
}

func NewService(recordingsDir string) recorderpb.RecorderServer {
	return &service{recordingsDir: recordingsDir}
}

func (svc *service) RecordStream(stream recorderpb.Recorder_RecordStreamServer) error {
	var sess *Session
	var n uint64

	for {
		frame, err := stream.Recv()
		if err == io.EOF { // gateway closed the stream cleanly
			if sess != nil {
				sess.close()
			}
			return stream.SendAndClose(&recorderpb.Ack{Frames: n})
		}
		if err != nil { // stream broke (gateway died / network). Flush what we have.
			if sess != nil {
				sess.close()
			}
			return err
		}

		if sess == nil { // first frame establishes the session
			sess, err = newSession(svc.recordingsDir, frame.SessionId)
			if err != nil {
				return err // can't even open the files → fail the stream
			}
			log.Printf("recorder: session %s started, dir=%s", sess.id, sess.dir)
		}

		if err := sess.write(frame.Kind, frame.TsUs, frame.CallUs, frame.Data); err != nil {
			// Invariant 4: a write failure degrades the recording, it does
			// NOT kill the call. Log, drop the frame, keep reading.
			log.Printf("recorder: session %s write %v: %v", sess.id, frame.Kind, err)
			continue
		}
		n++
	}
}
