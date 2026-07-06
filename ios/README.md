# SyncTrans iOS app (Milestone 3)

One-screen SwiftUI client for the relay: pick source/target languages, tap to
record, watch translated text scroll in. Audio is captured, resampled to 16 kHz
mono PCM16, and streamed to the relay over a WebSocket; transcription and
translation come back as JSON.

I can't compile Swift in the dev environment these files were written in, so they
ship as drop-in sources plus the setup steps below. Treat the first build as the
verification pass.

## Files

| File | Role |
|------|------|
| `SyncTrans/SyncTransApp.swift` | `@main` entry point |
| `SyncTrans/ContentView.swift` | the single screen (header + transcript + record button) |
| `SyncTrans/RelayClient.swift` | controller: WebSocket, protocol, recording lifecycle, state |
| `SyncTrans/AudioStreamer.swift` | mic capture → 16 kHz mono PCM16 |
| `SyncTrans/Models.swift` | `Segment`, `ServerMessage`, `Language` |
| `SyncTrans/Info.plist` | required permission / ATS keys (see below) |

## Xcode setup

1. **Create the project.** Xcode → New → Project → iOS → App. Name it
   `SyncTrans`, Interface **SwiftUI**, Language **Swift**. Minimum deployment
   **iOS 17** (the code uses `AVAudioApplication.requestRecordPermission` and the
   two-parameter `onChange`).
2. **Add the sources.** Delete the auto-generated `ContentView.swift` and
   `SyncTransApp.swift`, then drag the five `.swift` files from `SyncTrans/` into
   the project (check "Copy items if needed").
3. **Add the Info keys — via the Info tab, NOT by adding the file.** Do **not**
   drag `Info.plist` into the project (that causes a "Multiple commands produce
   Info.plist" build error — see Troubleshooting). It's a reference only. Leave
   "Generate Info.plist File" = Yes, then in the target's **Info** tab → "Custom
   iOS Target Properties" add:
   - **Privacy - Microphone Usage Description** (string)
   - **Privacy - Local Network Usage Description** (string)
   - **App Transport Security Settings** (Dictionary) → child **Allow Local
     Networking** (Boolean) = YES
   Without these the app crashes on mic use or silently fails to connect.
4. **Set the server address.** Either edit the default `host` in
   `RelayClient.swift`, or just type your Mac's LAN IP + port into the field at
   the top of the app at runtime, e.g. `192.168.1.23:8080`.

## Running it

1. **Start the relay** on your Mac:
   ```sh
   cd "go projects/synctrans"
   OPENAI_API_KEY=... ANTHROPIC_API_KEY=... go run ./cmd/relay
   ```
   It listens on `:8080`. Allow incoming connections if macOS firewall prompts.
2. **Find your Mac's LAN IP** (System Settings → Network, or `ipconfig getifaddr en0`).
3. **Run the app.** The iOS Simulator can use your Mac's microphone, so you can
   test without a device — but it must reach the relay: `localhost:8080` works
   from the Simulator; use the LAN IP from a physical phone (same Wi-Fi).
4. Set From/To languages, tap the mic, speak. You should see the live interim
   line, then committed source lines each followed by their translation.

## How it behaves (matches the relay protocol)

- **Languages** are sent as `?src=<iso>&tgt=<iso>` when the socket opens, so they
  apply with no extra handshake. Changing them requires reconnecting, so the
  pickers are locked while recording.
- **Low startup lag**: the audio session is activated on screen appear, so the
  record tap only does `engine.start()`; the WebSocket connects in parallel while
  audio is already being captured.
- **On stop** the app keeps the socket open for a few seconds so the last phrase
  and its translation still arrive, then disconnects. It does *not* send a stop
  frame (that would cancel the in-flight translation on the relay).

## Troubleshooting

- **"Multiple commands produce …/Info.plist"** — you added the `Info.plist` file
  to the target. Remove it: Project navigator → delete the `Info.plist` reference;
  Build Phases → Copy Bundle Resources → remove `Info.plist`. Keep "Generate
  Info.plist File" = Yes and add the keys via the Info tab (step 3 above).
- **Duplicate-symbol / weird link errors** — check for stray `… 2.swift` files
  (e.g. `ContentView 2.swift`) created by dragging a file into a folder that
  already had the template version. Delete the duplicate.
- **Preview canvas errors (`_main`, `SwiftUICore`, `CoreAudioTypes`)** — preview
  JIT noise, not a real build failure. Build/run with ⌘R instead; close the
  canvas or comment out the `#Preview` block if it keeps re-erroring.

## Known caveats

- The relay's upstream (OpenAI realtime) `session.update` schema is unverified
  against a live key — if transcription never arrives, check the relay logs and
  the `sessionUpdate` struct in `internal/stt/openai_realtime.go` first; the iOS
  side is likely fine.
- `ws://` is cleartext, fine for LAN personal use. For anything beyond that,
  serve `wss://` and drop the ATS exception (Milestone 4 territory, along with an
  auth token on the socket).
