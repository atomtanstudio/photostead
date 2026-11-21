// What was made with AI, as the web app sorts it: characters (formed from the LoRAs they were
// made with) and the prompt library (each distinct prompt once). Each opens its items from the
// server. Managing them (a character's LoRAs, naming) stays on the web.

import SwiftUI

struct CharactersView: View {
    private var browse: Browse { Browse.shared }

    var body: some View {
        ScrollView {
            if browse.characters.isEmpty {
                ContentUnavailableView("No characters yet", systemImage: "sparkles",
                                       description: Text("Characters are made on the web, from the LoRAs pictures were made with."))
                    .padding(.top, 60)
            }
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 150), spacing: 14)], spacing: 20) {
                ForEach(browse.characters) { c in
                    NavigationLink(value: Route.character(c)) { CharacterCard(character: c) }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("character")
                }
            }
            .padding(16)
        }
        .navigationTitle("Characters")
        .task { await browse.refresh() }
        .refreshable { await browse.refresh(force: true) }
    }
}

/// A character as four of their recent items, their name and how many.
struct CharacterCard: View {
    var character: CharacterRow

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Mosaic(samples: character.samples)
                .aspectRatio(1, contentMode: .fit)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            Text(character.name).font(.subheadline.weight(.semibold)).lineLimit(1)
            Text(character.count == 1 ? "1 item" : "\(character.count.formatted()) items").font(.caption).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Up to four items in a square: one fills it, more share it two by two.
private struct Mosaic: View {
    var samples: [CharacterRow.Sample]

    var body: some View {
        if samples.count < 2 {
            CoverPicture(id: samples.first?.id, hash: samples.first?.thumbhash, symbol: "sparkles")
        } else {
            let s = Array(samples.prefix(4))
            VStack(spacing: 2) {
                HStack(spacing: 2) { cell(s, 0); cell(s, 1) }
                HStack(spacing: 2) { cell(s, 2); cell(s, 3) }
            }
        }
    }

    private func cell(_ s: [CharacterRow.Sample], _ i: Int) -> some View {
        CoverPicture(id: s.indices.contains(i) ? s[i].id : nil, hash: s.indices.contains(i) ? s[i].thumbhash : nil, symbol: "sparkles")
    }
}

/// The prompt library: most used first, or the prompts with the words typed.
struct PromptsView: View {
    @Environment(Library.self) private var library
    @State private var prompts: [PromptRow]?
    @State private var total = 0
    @State private var text = ""
    @State private var problem = ""

    var body: some View {
        List {
            if let prompts {
                if prompts.isEmpty {
                    ContentUnavailableView(text.isEmpty ? "No prompts yet" : "No prompts with “\(text)”", systemImage: "text.quote",
                                           description: Text(text.isEmpty ? "Prompts are read from pictures made with AI." : ""))
                        .listRowBackground(Color.clear)
                }
                ForEach(prompts) { p in
                    NavigationLink(value: Route.prompt(p)) { PromptLine(prompt: p) }
                        .accessibilityIdentifier("prompt")
                }
                if total > prompts.count {
                    Section {} footer: { Text("The \(prompts.count) most used of \(total.formatted()). Search to find others.") }
                }
            } else if !problem.isEmpty {
                Text(problem).foregroundStyle(.secondary)
            } else {
                ProgressView().frame(maxWidth: .infinity).listRowBackground(Color.clear)
            }
        }
        .navigationTitle("Prompts")
        .searchable(text: $text, prompt: "Words in the prompt")
        .task(id: text) {
            if !text.isEmpty { try? await Task.sleep(for: .milliseconds(300)) } // typing: wait for a pause
            guard !Task.isCancelled else { return }
            await load()
        }
        .refreshable { await load() }
    }

    private func load() async {
        guard let api = library.api() else { return }
        do {
            let r = try await api.prompts(matching: text.trimmingCharacters(in: .whitespaces))
            prompts = r.prompts
            total = r.total
            problem = ""
        } catch {
            if prompts == nil { problem = error.localizedDescription }
        }
    }
}

private struct PromptLine: View {
    var prompt: PromptRow

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            CoverPicture(id: prompt.samples.first?.id, hash: prompt.samples.first?.thumbhash, symbol: "text.quote")
                .frame(width: 56, height: 56)
                .clipShape(RoundedRectangle(cornerRadius: 8))
            VStack(alignment: .leading, spacing: 4) {
                Text(prompt.text).font(.subheadline).lineLimit(3)
                Text(prompt.uses == 1 ? "Used once" : "Used \(prompt.uses.formatted()) times").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .combine)
    }
}
