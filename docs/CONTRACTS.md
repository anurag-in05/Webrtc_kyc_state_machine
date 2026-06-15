# Contracts (load-bearing — pinned exactly)

When media and decisions were one Python process, a refactor was a function
signature the compiler checked. Now these contracts cross language/process
boundaries and nothing type-checks them for you. Treat every value here as exact.

---

## 1. brain HTTP API (control plane)

Base path: `/api/v1/sessions`. JSON. The brain owns session state.

### POST `/start`  — called by browser

**Request — `StartSessionRequest` (copy this Pydantic model verbatim, it is the
current schema, NOT the README's):**

```python
class StartSessionRequest(BaseModel):
    language: Literal["english","hindi","bengali","gujarati","kannada","tamil","telugu"] = "english"
    company_id: int
    workspace_id: int
    room_name: str | None = None
    customer_name: str
    insured_name: str
    primary_mobile: str
    alternative_mobile: str | None = None
    email: str
    address: str
    company: str
    plan_name: str
    policy_term: int
    premium_amount: str
    premium_paying_term: int
    premium_frequency: str
    sum_insured: str
    application_date: str
    dob_life_assured: str
    free_look_period: int = 15
    currency: str = "INR"
    relation_to_insured: str = "myself"
    premium_payment_mode: str
    gender: str = ""
    nominee_name: str | None = None
    video_transport: Literal["ws","webrtc"] | None = None   # webrtc only now
```

**Response:**

```json
{
  "session_id": "hex",
  "state": "greeting",
  "agent_text": "Good morning, This is Kavya calling from ... Am I speaking with Mr Rahul Sharma?",
  "tts_plan": [ /* see §4 */ ],
  "voice_id": "21m00Tcm4TlvDq8ikWAM",
  "model_id": "eleven_flash_v2_5",
  "language": "english",
  "turn_index": 0,
  "ice_servers": [ {"urls":"stun:..."} , {"urls":"turn:...","username":"...","credential":"..."} ],
  "gateway_offer_url": "http://gateway:8080/sessions/{session_id}/offer",
  "capture_width": 640, "capture_height": 360, "capture_fps": 12,
  "video_enabled": true
}
```

### GET `/sessions/{id}`  — called by gateway (bootstrap) AND browser (poll)

Returns current session snapshot. The gateway reads `language` + the current
turn's `agent_text`/`tts_plan`/`voice_id`/`model_id` to play the greeting; the
browser reads `state`/`status`/`recording_status`/`full_call_video_url`.

```json
{
  "session_id":"hex", "state":"greeting", "language":"english", "turn_index":0,
  "agent_text":"...", "tts_plan":[...], "voice_id":"...", "model_id":"...",
  "status":"active",                        // active | completed | failed
  "events":[ {"event_type":"...","payload":{}} ],
  "recording_status":"pending",             // pending|complete|partial|audio_only|failed|disabled
  "full_call_video_url": null
}
```

### POST `/sessions/{id}/turn`  — called by GATEWAY (not the browser)

```json
// request
{ "transcript": "yes that's me" }
// response
{
  "state":"congrats", "intent":"yes", "transcript":"yes that's me",
  "agent_text":"...", "tts_plan":[...], "voice_id":"...", "model_id":"...",
  "turn_index":1, "attempt_count":0,
  "events":[ {"event_type":"record_verification_step","payload":{"step":"primary_mobile","status":"verified"}} ],
  "status":"active"
}
```

The brain: records the user turn, classifies intent (calls intent service),
runs `flow.step(intent)`, records the agent turn, builds the next `tts_plan`.
When `state` becomes `completed`, `agent_text` is `""` and `tts_plan` is `[]`
(nothing left to say). Intent classification failure → `intent="please_repeat"`.

### POST `/sessions/{id}/end`  — called by browser

Marks the session terminal (forces `failed` if not already terminal), writes
`transcript.txt` + `transcript.json`, then calls gateway `POST /close`.

```json
{ "state":"completed", "turn_index":12,
  "transcript_url":"...", "transcript_json_url":"...",
  "recording_status":"pending" }
```

### Events (emit exactly these `event_type` strings — copied from the state machine)

`record_verification_step` (payload `{step, status}`), `greeting_finished`,
`customer_ready`, `declaration_recorded`, `consent_verified` (payload
`{verified: bool}`), `session_completed`, `session_failed`.

Verification step names: `client_declaration`, `primary_mobile`,
`alternative_mobile`, `email_id`, `address`, `medical_fitness`,
`no_policy_merging`, `own_discretion`, `final_satisfaction`.

---

## 2. gateway HTTP/WS API (media plane)

### POST `/sessions/{id}/offer`  — browser
`{"sdp":"...","type":"offer"}` → `{"sdp":"...","type":"answer"}`.
The peer is **send-only**: the browser publishes mic + camera and subscribes to
nothing (there is no outbound media track — pure-Go means no Opus encoder). The
agent voice is delivered over the control WS as binary frames (below).
Also accepts a re-offer with ICE restart on the existing peer (recover a network
change without splitting the recording). 404 unknown session, 410 terminal.

### WS `/sessions/{id}/control`  — browser ⇄ gateway

Reuse the JSON protocol from the old `web/index.html`. **Plus**: the gateway
sends the agent's voice as **binary frames** on this same socket — raw s16le mono
48 kHz PCM — which the browser plays via Web Audio. Text frames are JSON control;
binary frames are agent audio. (This replaces the old inbound WebRTC audio track.)

| Dir | Message |
|---|---|
| client → gateway | `{"type":"start_turn"}` — begin listening for one turn |
| client → gateway | `{"type":"end"}` — tear down |
| gateway → client | `{"type":"vad","signal":"START_SPEECH"\|"END_SPEECH","at":<float>}` |
| gateway → client | `{"type":"final", ...TurnResponse fields...}` — UI updates |
| gateway → client | `{"type":"agent_done","turn_index":N,"status":"active"}` — client may start next turn |
| gateway → client | `{"type":"error","message":"..."}` |

Loop (interpretation A — gateway drives): speak greeting → `agent_done` →
wait for `start_turn` → stream STT → on END_SPEECH, `POST brain /turn` → send
`final` → speak reply → `agent_done`. Repeat until `status != active`.

### POST `/sessions/{id}/close`  — brain (internal)
Close the peer (flush recorder, then `recorder POST /finalize`). Idempotent.

---

## 3. recorder API (media plane)

### gRPC `RecordStream(stream Frame) returns (Ack)`  — gateway → recorder

```proto
// proto/recorder.proto
syntax = "proto3";
package recorder;
enum Kind { VIDEO_AU = 0; USER_PCM = 1; AGENT_PCM = 2; }
message Frame {
  string session_id = 1;
  Kind   kind       = 2;
  uint64 ts_us      = 3;   // monotonic microseconds from first frame of this stream
  bytes  data       = 4;   // VIDEO_AU: one H264 access unit (Annex-B). *_PCM: s16le mono 48kHz
}
message Ack { uint64 frames = 1; }
```

One stream per session. The first `VIDEO_AU` the recorder keeps must be a
keyframe (drop until one arrives). PCM is 48 kHz mono s16le for both kinds.

### POST `/sessions/{id}/finalize`  — gateway
Kicks the ffmpeg combine + S3 upload (async). Returns immediately.

### GET `/sessions/{id}/status`  — brain (on poll)
`{"status":"pending|complete|partial|audio_only|failed","url":"<mp4 url or null>"}`

---

## 4. The `tts_plan` (brain → gateway)

The brain builds this by reusing the existing `tts.py` splitting logic but
**stopping before the HTTP call**. An ordered list; the gateway executes it:

```json
[
  {"kind":"speech","text":"Is your mobile number","slow":false,"speed":1.0},
  {"kind":"silence","ms":200},
  {"kind":"speech","text":"nine, eight, seven, six, five.","slow":true},
  {"kind":"silence","ms":200},
  {"kind":"speech","text":"?","slow":false,"speed":1.0}
]
```

How the brain builds it (port of `tts.synthesize_stream`, minus the network):
- `parts = re.split(r"(<var>.*?</var>)", agent_text, flags=re.DOTALL)`
- non-`<var>` part → `{"kind":"speech","text":clean,"slow":false,"speed":body_speed}`
- `<var>` part → `{silence 200}`, then `{"kind":"speech","text":_slow_down_var_text(clean),"slow":true}`, then `{silence 200}`
- `body_speed = tts_speed_long (1.1) if spoken_len(agent_text) >= 200 else tts_speed (1.0)`
- `_slow_down_var_text(raw)` = `", ".join(words) + "."`
- drop empty/whitespace-only segments

`voice_id` / `model_id` are resolved by the brain (i18n pack → env → default) and
returned alongside the plan. The gateway holds **no** markup logic.

---

## 5. Vendor protocols the gateway must port (pinned from the Python source)

### Sarvam STT — streaming WebSocket (port of `pipeline/stt.py:transcribe_stream`)

- URL: `wss://api.sarvam.ai/speech-to-text/ws?` + query:
  `language-code=<bcp47>&model=saarika:v2.5&sample_rate=16000&input_audio_codec=pcm_s16le&vad_signals=true`
- Header: `Api-Subscription-Key: <key>`
- Send audio (per chunk): `{"audio":{"data":"<base64 pcm s16le 16k mono>","sample_rate":"16000","encoding":"audio/wav"}}`
- On END_SPEECH (or end of turn): send `{"type":"flush"}`
- Receive:
  - `{"type":"events","data":{"signal_type":"START_SPEECH"|"END_SPEECH","occured_at":<float>}}` → vad
  - `{"type":"data","data":{"transcript":"..."}}` → final transcript (one, then done)
  - `{"type":"error","data":{"error":"..."}}` → error
- Hard wall-clock timeout **15 s** from connect → if no `data`, emit error.
- `bcp47` map (`pipeline/sarvam_lang.py`, copy verbatim): english→en-IN, hindi→hi-IN,
  bengali→bn-IN, gujarati→gu-IN, kannada→kn-IN, tamil→ta-IN, telugu→te-IN
  (+ short codes; default en-IN).

### ElevenLabs TTS — HTTP streaming (port of `pipeline/tts.py:_stream_one`)

- URL: `https://api.elevenlabs.io/v1/text-to-speech/{voice_id}/stream?output_format=pcm_24000`
- Headers: `xi-api-key: <key>`, `Content-Type: application/json`
- Payload, **normal** speech segment (`slow=false`):
  ```json
  {"text":"<segment>","model_id":"<model_id>",
   "voice_settings":{"stability":0.5,"similarity_boost":0.75,"style":0.0,"use_speaker_boost":true,"speed":<speed>}}
  ```
- Payload, **slow** segment (`slow=true`, the `<var>` digits):
  ```json
  {"text":"<segment>","model_id":"<model_id>",
   "voice_settings":{"stability":0.7,"similarity_boost":0.75,"style":0.0,"use_speaker_boost":true}}
  ```
  (note: slow segments send **no** `speed` field)
- Response body is raw 24 kHz mono s16le PCM. Read in chunks, resample to 48k,
  send as binary frames on the control WS (and tee to the recorder).
- `{"kind":"silence","ms":N}` → emit `24000 * N/1000 * 2` zero bytes (no HTTP call).
- Defaults: `voice_id=21m00Tcm4TlvDq8ikWAM`, `model_id=eleven_flash_v2_5`.

---

## 6. intent service — UNCHANGED

`POST /classify {"text":"...","language":"english"}` → `{"intent":"yes|no|please_repeat"}`.
The brain calls it (existing `RemoteIntentClassifier`). Any failure → `please_repeat`.
