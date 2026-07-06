import SwiftUI

// ContentView is the entire UI: a config header, a scrolling transcript, and a
// single record button at the bottom.
struct ContentView: View {
    @StateObject private var client = RelayClient()

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            transcript
            Divider()
            recordBar
        }
        .onAppear { client.onAppear() }
    }

    // MARK: - Header (server + language config)

    private var header: some View {
        VStack(spacing: 10) {
            HStack(spacing: 8) {
                Circle()
                    .fill(client.isConnected ? Color.green : Color.gray)
                    .frame(width: 10, height: 10)
                TextField("server ip:port", text: $client.host)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .textFieldStyle(.roundedBorder)
                    .font(.callout)
            }

            HStack(spacing: 8) {
                languagePicker(selection: $client.sourceLang)
                Image(systemName: "arrow.right")
                    .foregroundStyle(.secondary)
                languagePicker(selection: $client.targetLang)
                Spacer()
                Button("Clear") { client.clear() }
                    .font(.callout)
            }

            Text(client.status)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding()
        // Lock language/server changes while recording — they require a new
        // connection, so changing them mid-talk would be lost anyway.
        .disabled(client.isRecording)
    }

    private func languagePicker(selection: Binding<Language>) -> some View {
        Picker("", selection: selection) {
            ForEach(Language.all) { Text($0.name).tag($0) }
        }
        .pickerStyle(.menu)
        .labelsHidden()
    }

    // MARK: - Transcript

    private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    ForEach(client.segments) { seg in
                        VStack(alignment: .leading, spacing: 3) {
                            Text(seg.source)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Text(seg.translation ?? "…")
                                .font(.body)
                                .foregroundStyle(seg.translation == nil ? .secondary : .primary)
                        }
                    }
                    if !client.interim.isEmpty {
                        Text(client.interim)
                            .font(.body)
                            .italic()
                            .foregroundStyle(.secondary)
                    }
                    // Anchor we always scroll to so newest text stays in view.
                    Color.clear.frame(height: 1).id("BOTTOM")
                }
                .padding()
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .onChange(of: client.segments.count) { _, _ in
                withAnimation { proxy.scrollTo("BOTTOM", anchor: .bottom) }
            }
            .onChange(of: client.interim) { _, _ in
                proxy.scrollTo("BOTTOM", anchor: .bottom)
            }
        }
    }

    // MARK: - Record button

    private var recordBar: some View {
        Button(action: client.toggle) {
            ZStack {
                Circle()
                    .fill(client.isRecording ? Color.red : Color.accentColor)
                    .frame(width: 72, height: 72)
                Image(systemName: client.isRecording ? "stop.fill" : "mic.fill")
                    .font(.system(size: 28, weight: .semibold))
                    .foregroundStyle(.white)
            }
        }
        .padding(.vertical, 12)
    }
}

#Preview {
    ContentView()
}
