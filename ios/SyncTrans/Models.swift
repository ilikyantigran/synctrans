import Foundation

// Segment is one finalized phrase: the source-language transcription plus its
// translation (which arrives a moment later, matched by id == relay SegID).
struct Segment: Identifiable {
    let id: UInt64
    let source: String
    var translation: String?
}

// ServerMessage mirrors the relay's protocol.ServerMessage (internal/protocol).
// Keep these field names in sync with the Go side.
struct ServerMessage: Decodable {
    let type: String      // "ready" | "interim" | "segment" | "translation" | "error"
    let segId: UInt64?
    let text: String?
    let lang: String?
    let message: String?
}

// Language is a pickable source/target option. `code` is the ISO-639-1 value the
// relay expects in the ?src=/?tgt= query params.
struct Language: Identifiable, Hashable {
    let code: String
    let name: String
    var id: String { code }

    static let hebrew    = Language(code: "he", name: "Hebrew")
    static let english   = Language(code: "en", name: "English")
    static let russian   = Language(code: "ru", name: "Russian")
    static let arabic    = Language(code: "ar", name: "Arabic")
    static let spanish   = Language(code: "es", name: "Spanish")
    static let french    = Language(code: "fr", name: "French")
    static let german    = Language(code: "de", name: "German")
    static let ukrainian = Language(code: "uk", name: "Ukrainian")

    // Must match the relay's languageNames map (internal/translate) for the
    // target language to get a clean prompt; unknown codes still work.
    static let all: [Language] = [
        hebrew, english, russian, arabic, spanish, french, german, ukrainian,
    ]
}
