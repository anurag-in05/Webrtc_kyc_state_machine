// Package config holds the gateway's environment configuration. The full set is
// pinned in docs/GATEWAY.md ("Config (env)"); nothing else is read.
package config

import "os"

type Config struct {
	Port             string // PORT (default 8080)
	BrainURL         string // BRAIN_URL — brain HTTP base (CONTRACTS §1)
	RecorderGRPCAddr string // RECORDER_GRPC_ADDR — recorder RecordStream (CONTRACTS §3)
	RecorderHTTPURL  string // RECORDER_HTTP_URL — recorder /finalize, /status
	SarvamAPIKey     string // SARVAM_API_KEY — Sarvam STT (CONTRACTS §5)
	ElevenLabsAPIKey string // ELEVENLABS_API_KEY — ElevenLabs TTS (CONTRACTS §5)
	TURNURL          string // TURN_URL
	TURNUsername     string // TURN_USERNAME
	TURNCredential   string // TURN_CREDENTIAL
}

// Load reads the config from the environment. Localhost defaults keep the
// gateway runnable in dev without every key set; vendor keys default empty.
func Load() Config {
	return Config{
		Port:             env("PORT", "8080"),
		BrainURL:         env("BRAIN_URL", "http://localhost:8000"),
		RecorderGRPCAddr: env("RECORDER_GRPC_ADDR", "localhost:9091"),
		RecorderHTTPURL:  env("RECORDER_HTTP_URL", "http://localhost:9090"),
		SarvamAPIKey:     os.Getenv("SARVAM_API_KEY"),
		ElevenLabsAPIKey: os.Getenv("ELEVENLABS_API_KEY"),
		TURNURL:          os.Getenv("TURN_URL"),
		TURNUsername:     os.Getenv("TURN_USERNAME"),
		TURNCredential:   os.Getenv("TURN_CREDENTIAL"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
