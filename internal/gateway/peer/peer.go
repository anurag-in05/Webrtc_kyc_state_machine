package peer

import (
	"encoding/binary"
	"fmt"
	"log"
	"sync"

	"github.com/pion/interceptor"
	"github.com/pion/opus"
	"github.com/pion/webrtc/v4"

	"kyc-monorepo/internal/gateway/audio"
	"kyc-monorepo/internal/gateway/recordclient"
	recorderpb "kyc-monorepo/proto"
)

// Config carries the ICE settings the gateway's peer needs (GATEWAY.md env).
type Config struct {
	TURNURL        string
	TURNUsername   string
	TURNCredential string
}

// Peer is the gateway side of the one WebRTC peer per call. The browser is
// send-only — it publishes mic + camera and subscribes to nothing — so there is
// NO outbound media track here (the agent voice leaves over the control WS, and
// pure-Go means no Opus encoder). Pion handles ICE/DTLS; we wire OnTrack to tee
// the inbound video and user audio to the recorder. Not an SFU: no forwarding.
type Peer struct {
	pc  *webrtc.PeerConnection
	rec *recordclient.Client
	wg  sync.WaitGroup // OnTrack read goroutines; Close waits on them

	micMu sync.Mutex
	mic   func([]byte) // 16 kHz mic handler for the active turn; nil = discard
}

// SetMic sets (nil clears) the 16 kHz mic handler — the listening-window gate. The
// turn loop sets it to the active turn's stt.MicBuffer.Push at start_turn and
// clears it at END_SPEECH, so STT hears the mic only during a turn, never the idle
// audio between turns. Concurrency-safe (the audio reader calls feedMic; the turn
// loop calls SetMic).
func (p *Peer) SetMic(h func([]byte)) {
	p.micMu.Lock()
	p.mic = h
	p.micMu.Unlock()
}

// feedMic delivers a 16 kHz frame to the active handler UNDER the lock, so that
// SetMic(nil) is a barrier: once it returns, no handler call can still be in
// flight. The turn loop relies on this to do SetMic(nil) then mic.CloseInput()
// without the audio reader ever pushing to a closed MicBuffer. (The handler —
// MicBuffer.Push — is non-blocking, so holding the lock is bounded.)
func (p *Peer) feedMic(pcm16 []byte) {
	p.micMu.Lock()
	defer p.micMu.Unlock()
	if p.mic != nil {
		p.mic(pcm16)
	}
}

// New creates the peer from the browser's send-only offer, wires the OnTrack
// media tees, and returns the answer SDP (with all ICE candidates — non-trickle,
// the offer is a single POST round-trip per CONTRACTS §2). ICE/DTLS proceed in
// the background. onClose is invoked at most once when the connection reaches a
// terminal state (CONTRACTS §2: "On /close OR terminal peer state"), so an
// unexpected disconnect tears the call down without waiting for /close.
func New(offerSDP string, cfg Config, rec *recordclient.Client, onClose func()) (answerSDP string, p *Peer, err error) {
	api, err := newAPI()
	if err != nil {
		return "", nil, fmt.Errorf("peer: media engine: %w", err)
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers(cfg)})
	if err != nil {
		return "", nil, fmt.Errorf("peer: new connection: %w", err)
	}
	p = &Peer{pc: pc, rec: rec}
	pc.OnTrack(p.onTrack)
	var closeOnce sync.Once
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("peer: connection state %s", s)
		// Failed/Closed only — Disconnected can still recover, so we don't tear
		// down on it. Minimal: no timers/grace period (G7's teardown phase refines).
		if onClose != nil && (s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed) {
			closeOnce.Do(onClose)
		}
	})
	answerSDP, err = p.answer(offerSDP)
	if err != nil {
		pc.Close()
		return "", nil, err
	}
	return answerSDP, p, nil
}

// Reoffer applies a re-offer (network change / ICE restart) to the existing peer
// without tearing down the recording (CONTRACTS §2): same answer flow on the
// same PeerConnection.
func (p *Peer) Reoffer(offerSDP string) (string, error) {
	return p.answer(offerSDP)
}

// answer sets the remote offer, creates+sets the local answer, and blocks until
// ICE gathering completes so the returned SDP carries every candidate.
func (p *Peer) answer(offerSDP string) (string, error) {
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: offerSDP,
	}); err != nil {
		return "", fmt.Errorf("peer: set remote: %w", err)
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("peer: create answer: %w", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("peer: set local: %w", err)
	}
	<-gatherComplete
	return p.pc.LocalDescription().SDP, nil
}

func (p *Peer) onTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	log.Printf("peer: inbound track kind=%s codec=%s", track.Kind(), track.Codec().MimeType)
	p.wg.Add(1)
	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		go p.readVideo(track)
	case webrtc.RTPCodecTypeAudio:
		go p.readAudio(track)
	default:
		p.wg.Done()
	}
}

// readVideo turns the video track's RTP into Annex-B access units and tees them
// as VIDEO_AU. No decode. On loop exit (track end / peer close) it flushes the
// final buffered AU — this goroutine owns the assembler and is its only flush
// site (see h264.go teardown note).
func (p *Peer) readVideo(track *webrtc.TrackRemote) {
	defer p.wg.Done()
	var asm h264Assembler
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			break // track ended / peer closed
		}
		if au := asm.push(pkt); au != nil {
			p.rec.Send(recorderpb.Kind_VIDEO_AU, au)
		}
	}
	if au := asm.flush(); au != nil {
		p.rec.Send(recorderpb.Kind_VIDEO_AU, au)
	}
}

// readAudio decodes the inbound Opus mic to 48 kHz mono s16le (pure-Go, no
// encoder), tees it as USER_PCM, and feeds a 48k→16k downsample to the active
// turn's mic (the listening-window gate). One Downsampler per track keeps the
// decimation phase continuous; idle frames (no turn active) are discarded.
func (p *Peer) readAudio(track *webrtc.TrackRemote) {
	defer p.wg.Done()
	dec := opus.NewDecoder() // 48 kHz mono output
	var down audio.Downsampler
	out := make([]int16, maxOpusFrameSamples)
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			break
		}
		n, err := dec.DecodeToInt16(pkt.Payload, out)
		if err != nil {
			continue // bad/again packet → drop one frame (degrade, never break)
		}
		pcm48 := int16ToS16LE(out[:n])
		p.rec.Send(recorderpb.Kind_USER_PCM, pcm48)
		p.feedMic(down.Down48to16(pcm48)) // → active turn's MicBuffer, or discarded
	}
}

// Close shuts the peer and waits for the OnTrack read goroutines to exit, so
// their final Sends (including the flushed video tail) are enqueued before the
// caller closes the recordclient stream. Pion's Close is idempotent.
func (p *Peer) Close() {
	p.pc.Close() // unblocks ReadRTP → read loops break and flush
	p.wg.Wait()  // their final Sends are now done
}

// maxOpusFrameSamples bounds one decoded Opus frame: 120 ms (Opus max) at 48 kHz
// mono = 5760 samples.
const maxOpusFrameSamples = 48000 / 1000 * 120

func int16ToS16LE(s []int16) []byte {
	b := make([]byte, len(s)*2)
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

// newAPI builds a Pion API whose MediaEngine advertises ONLY Opus (mic) and H264
// (camera). Restricting video to H264 is load-bearing: readVideo depacketizes with
// the H264 assembler (h264.go) and the recorder writes H264 into fragmented MP4.
// Pion's default codecs ALSO offer VP8/VP9, which Chrome picks first by default —
// delivering a track the H264 assembler can't parse, so no access units are ever
// written and the recording is silently audio-only. packetization-mode=1 matches
// the FU-A/STAP-A reassembly in h264.go; profile 42e01f is Constrained Baseline,
// which every browser can encode. Default interceptors keep NACK/RTCP behaviour.
func newAPI() (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"},
		PayloadType:        102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	i := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		return nil, err
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i)), nil
}

func iceServers(cfg Config) []webrtc.ICEServer {
	servers := []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}
	if cfg.TURNURL != "" {
		servers = append(servers, webrtc.ICEServer{
			URLs:       []string{cfg.TURNURL},
			Username:   cfg.TURNUsername,
			Credential: cfg.TURNCredential,
		})
	}
	return servers
}
