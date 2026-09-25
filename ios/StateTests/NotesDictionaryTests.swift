import XCTest
@testable import State

/// The dictionary the owner keeps for voice notes: the import reads the
/// same format as the server's seed file, and saving sends the revision it
/// was edited from.
final class NotesDictionaryTests: XCTestCase {
    func testImportReadsTheServerFormat() throws {
        let parsed = try XCTUnwrap(NotesDictionaryImport.parse("# Kommentar\nword: Supabase\n\nalways: ZEVDISK -> sevDesk\ncontext: Note -> Node\nWispr Flow\nAlicid->Elicit\n"))
        XCTAssertEqual(parsed.words, ["Supabase", "Wispr Flow"])
        XCTAssertEqual(parsed.corrections, [
            DictionaryCorrection(from: "ZEVDISK", to: "sevDesk", mode: .always),
            DictionaryCorrection(from: "Note", to: "Node", mode: .context),
            DictionaryCorrection(from: "Alicid", to: "Elicit", mode: .context),
        ])
    }

    func testImportRefusesUnknownLines() {
        XCTAssertNil(NotesDictionaryImport.parse("sometimes: A -> B"))
        XCTAssertNil(NotesDictionaryImport.parse("always: nur ein Wort"))
        XCTAssertNil(NotesDictionaryImport.parse("always:  -> B"))
    }

    func testImportMergesWithoutDuplicates() throws {
        var dictionary = NotesDictionary(words: ["Supabase"], corrections: [DictionaryCorrection(from: "ZEVDISK", to: "sevDesk", mode: .always)], revision: 3)
        let parsed = try XCTUnwrap(NotesDictionaryImport.parse("word: supabase\nword: Vercel\nalways: zevdisk -> sevDesk\ncontext: Cloud -> Claude"))
        dictionary.merge(parsed)
        XCTAssertEqual(dictionary.words, ["Supabase", "Vercel"])
        XCTAssertEqual(dictionary.corrections.map(\.from), ["ZEVDISK", "Cloud"])
    }

    func testSavePayloadCarriesTheRevision() throws {
        let dictionary = NotesDictionary(words: ["Supabase"], corrections: [DictionaryCorrection(from: "Note", to: "Node", mode: .context)], revision: 4)
        let body = try dictionary.updatePayload()
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertEqual(json["expected_revision"] as? Int, 4)
        XCTAssertEqual(json["words"] as? [String], ["Supabase"])
        let corrections = try XCTUnwrap(json["corrections"] as? [[String: String]])
        XCTAssertEqual(corrections, [["from": "Note", "to": "Node", "mode": "context"]])
    }

    func testDecodesTheServerDictionary() throws {
        let json = #"{"words":["BLUNATECH"],"corrections":[{"from":"Q3D","to":"QDRANT","mode":"always"}],"revision":2,"updated_at":"2026-09-25T12:00:00Z"}"#
        let dictionary = try StateJSON.decoder.decode(NotesDictionary.self, from: Data(json.utf8))
        XCTAssertEqual(dictionary.revision, 2)
        XCTAssertEqual(dictionary.corrections.first?.mode, .always)
    }
}
