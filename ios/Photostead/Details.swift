// An item's details, from a swipe up or the (i) in the viewer, laid out as the iPhone's Photos lays
// out its own: a caption, when, the camera (with the exposure), where (on a map), who's in it; then
// how an AI picture was made, the text in it, its albums and where its file is kept. What the phone
// has shows at once; the rest comes from the server. People, places, albums, models and LoRAs open
// their collections in the sheet itself.

import MapKit
import SwiftUI

struct Details: View {
    var item: Item
    @Environment(\.dismiss) private var dismiss
    @Environment(Library.self) private var library
    @State private var details: ItemDetails?
    @State private var problem = ""
    @State private var path = NavigationPath()
    @State private var detent: PresentationDetent = .medium

    var body: some View {
        NavigationStack(path: $path) {
            List {
                if let d = details { caption(d) }
                when
                card
                if let d = details {
                    place(d)
                    people(d)
                    if let g = d.generation { GenerationSection(g: g) }
                    if let text = d.text?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty {
                        Section("Text in the picture") { LongText(text: text, copy: "Copy the text") }
                    }
                    belongs(d)
                    file(d)
                } else if !problem.isEmpty {
                    Section { Text(problem).font(.footnote).foregroundStyle(.secondary) }
                } else {
                    Section { ProgressView().frame(maxWidth: .infinity) }
                }
                if library.organize { MarksEditor(item: item) }
            }
            .navigationTitle("Details")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .navigationDestination(for: Route.self) { destination($0) }
            .task { await load() }
        }
        .presentationDetents([.medium, .large], selection: $detent)
        .onChange(of: path.count) { if path.count > 0 { withAnimation { detent = .large } } } // a collection wants the room
    }

    private func load() async {
        guard details == nil else { return }
        guard let api = library.api() else {
            problem = "The rest of its details come from Photostead when it’s reachable."
            return
        }
        do {
            details = try await api.details(item.id)
        } catch {
            problem = "Couldn’t get the rest of its details: \(error.localizedDescription)"
        }
    }

    // MARK: Sections

    /// Its title, description and keywords, as written for it (or brought over by an import).
    @ViewBuilder
    private func caption(_ d: ItemDetails) -> some View {
        let title = d.title?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let text = d.description?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let words = d.keywords ?? []
        if !title.isEmpty || !text.isEmpty || !words.isEmpty {
            Section {
                if !title.isEmpty { Text(title).font(.headline) }
                if !text.isEmpty { Text(text).textSelection(.enabled) }
                if !words.isEmpty {
                    Text(words.map { "#\($0)" }.joined(separator: "  "))
                        .foregroundStyle(.secondary)
                        .accessibilityLabel("Keywords: \(words.joined(separator: ", "))")
                }
            }
        }
    }

    /// When it was taken, on the clock where it was taken; and whether it's a Live Photo or a favorite.
    private var when: some View {
        let date = details?.takenAt ?? item.takenAt, offset = details?.offsetMin
        return Section {
            VStack(alignment: .leading, spacing: 3) {
                Text(DetailText.day(date, offsetMin: offset)).font(.headline)
                Text(DetailText.time(date, offsetMin: offset)).foregroundStyle(.secondary)
                if let note = DetailText.dateNote(details?.dateSource) { Text(note).font(.footnote).foregroundStyle(.secondary) }
            }
            .accessibilityElement(children: .combine)
            if item.motionID != nil {
                Label {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Live Photo")
                        Text("Press and hold the photo to play it.").font(.footnote).foregroundStyle(.secondary)
                    }
                } icon: { Image(systemName: "livephoto") }
                .accessibilityElement(children: .combine)
            }
            if item.favorite { Label("Favorite", systemImage: "heart.fill") }
            if details?.archived == true { Label("Archived", systemImage: "archivebox") }
        }
    }

    /// The camera and lens (or the file, when no camera's known), its size, and the exposure in a
    /// strip along the bottom, as Photos shows it.
    private var card: some View {
        let d = details
        let name = d?.fileName ?? item.fileName
        let camera = d?.camera.flatMap { DetailText.camera(make: $0.make, model: $0.model) }
        let lens = d?.camera?.lens.flatMap { $0.isEmpty ? nil : DetailText.lens($0, model: d?.camera?.model) }
        let size = DetailText.size(width: d?.width ?? item.width, height: d?.height ?? item.height, video: item.isVideo,
                                   durationMs: d?.durationMs ?? item.durationMs, bytes: d?.fileSize)
        let exposure = d?.camera.map { DetailText.exposure($0) } ?? []
        return Section {
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(camera ?? (name.isEmpty ? (item.isVideo ? "Video" : "Photo") : name))
                        .font(.headline).lineLimit(3).truncationMode(.middle)
                    Spacer(minLength: 0)
                    if let format = DetailText.format(fileName: name) {
                        Text(format).font(.caption2.weight(.semibold)).foregroundStyle(.secondary)
                            .padding(.horizontal, 6).padding(.vertical, 2)
                            .background(.quaternary, in: RoundedRectangle(cornerRadius: 5))
                            .fixedSize()
                    }
                }
                if let lens { Text(lens).font(.subheadline).foregroundStyle(.secondary) }
                if camera != nil && !name.isEmpty { Text(name).font(.subheadline).foregroundStyle(.secondary).lineLimit(2).truncationMode(.middle) }
                if !size.isEmpty { Text(size).font(.subheadline.monospacedDigit()).foregroundStyle(.secondary) }
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("details-card")
            if !exposure.isEmpty { ExposureStrip(values: exposure) }
        }
    }

    /// The place's name (its items, a tap away) and a map; the coordinates only when it has no name.
    @ViewBuilder
    private func place(_ d: ItemDetails) -> some View {
        if let at = d.location {
            let name = at.place?.trimmingCharacters(in: .whitespaces) ?? ""
            Section("Where") {
                if name.isEmpty {
                    Label(DetailText.coordinates(lat: at.lat, lon: at.lon), systemImage: "mappin.and.ellipse").monospacedDigit()
                } else if let id = at.placeId {
                    NavigationLink(value: Route.remote(title: name.components(separatedBy: ",").first ?? name, collection: "place:\(id)")) {
                        Label(name, systemImage: "mappin.and.ellipse")
                    }
                    .accessibilityHint("Shows everything taken here")
                } else {
                    Label(name, systemImage: "mappin.and.ellipse")
                }
                MapPreview(lat: at.lat, lon: at.lon, name: name)
                    .listRowInsets(EdgeInsets())
            }
        }
    }

    /// Who's in it: people and pets, recognized or suggested, each opening their items.
    @ViewBuilder
    private func people(_ d: ItemDetails) -> some View {
        let refs = d.people ?? []
        if !refs.isEmpty {
            let pets = refs.filter { $0.kind == "pet" }.count
            Section(pets == 0 ? "People" : pets == refs.count ? (pets == 1 ? "Pet" : "Pets") : "People and pets") {
                ForEach(refs) { ref in
                    let known = library.people.first { $0.id == ref.id }
                    NavigationLink(value: Route.person(known ?? Person(id: ref.id, name: ref.name, count: 0, kind: ref.kind))) {
                        PersonRow(ref: ref, person: known)
                    }
                }
            }
        }
    }

    /// The albums it's in, and the AI characters it shows.
    @ViewBuilder
    private func belongs(_ d: ItemDetails) -> some View {
        let albums = d.albums ?? [], characters = d.characters ?? []
        if !albums.isEmpty {
            Section("In albums") {
                ForEach(albums) { a in
                    if let row = library.albums.first(where: { $0.id == a.id }) {
                        NavigationLink(value: Route.album(row)) { Label(a.name, systemImage: "rectangle.stack") }
                    } else {
                        Label(a.name, systemImage: "rectangle.stack")
                    }
                }
            }
        }
        if !characters.isEmpty {
            Section("Characters") {
                ForEach(characters) { c in
                    NavigationLink(value: Route.remote(title: c.name, collection: "character:\(c.id)")) { Label(c.name, systemImage: "sparkles") }
                }
            }
        }
    }

    /// Where the file is kept, whose it is when it's someone else's, and whether it was edited.
    @ViewBuilder
    private func file(_ d: ItemDetails) -> some View {
        let folder = [d.library, d.folderPath.flatMap { $0 == "." ? nil : $0 }].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " / ")
        if !folder.isEmpty || d.owner != nil || d.shared == true || d.edited == true {
            Section("File") {
                if !folder.isEmpty { Label(folder, systemImage: "folder").textSelection(.enabled) }
                if let owner = d.owner {
                    Label("Shared with the Household by \(owner.name)", systemImage: "house")
                } else if d.shared == true {
                    Label("Shared with the Household", systemImage: "house")
                }
                if d.edited == true { Label("Edited on the web. Saving it gets the edited photo.", systemImage: "slider.horizontal.3") }
            }
            .foregroundStyle(.secondary)
        }
    }
}

/// Aperture, shutter, focal length and ISO side by side; on one line, joined, when they don't fit.
private struct ExposureStrip: View {
    var values: [String]

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: 0) {
                ForEach(Array(values.enumerated()), id: \.offset) { i, v in
                    if i > 0 { Divider().frame(height: 16) }
                    Text(v).lineLimit(1).fixedSize().frame(maxWidth: .infinity)
                }
            }
            Text(DetailText.together(values))
        }
        .font(.subheadline.monospacedDigit())
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Exposure")
        .accessibilityValue(values.joined(separator: ", "))
        .accessibilityIdentifier("exposure")
    }
}

/// Where it was taken, on a map that stays put; a tap opens it in Maps.
private struct MapPreview: View {
    var lat: Double
    var lon: Double
    var name: String
    @Environment(\.openURL) private var openURL

    var body: some View {
        let at = CLLocationCoordinate2D(latitude: lat, longitude: lon)
        Button {
            var maps = URLComponents(string: "https://maps.apple.com/")!
            maps.queryItems = [URLQueryItem(name: "ll", value: "\(lat),\(lon)"), URLQueryItem(name: "q", value: name.isEmpty ? "Photo" : name)]
            if let url = maps.url { openURL(url) }
        } label: {
            Map(initialPosition: .region(MKCoordinateRegion(center: at, latitudinalMeters: 1800, longitudinalMeters: 1800)), interactionModes: []) {
                Marker("", coordinate: at)
            }
            .mapStyle(.standard(pointsOfInterest: .excludingAll))
            .frame(height: 160)
            .allowsHitTesting(false)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(name.isEmpty ? "Map" : "Map of \(name)")
        .accessibilityHint("Opens it in Maps")
    }
}

/// Someone (or a pet) in it: their face when Photostead has one, and whether it's only a suggestion.
private struct PersonRow: View {
    var ref: ItemDetails.Ref
    var person: Person?

    private var pet: Bool { ref.kind == "pet" }

    var body: some View {
        HStack(spacing: 12) {
            Group {
                if let person, person.coverFace != nil {
                    FacePicture(person: person)
                } else {
                    Circle().fill(.quaternary).overlay { Image(systemName: pet ? "pawprint.fill" : "person.fill").foregroundStyle(.secondary) }
                }
            }
            .frame(width: 36, height: 36).clipShape(Circle())
            .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 1) {
                Text(ref.name.isEmpty ? (pet ? "Unnamed pet" : "Not named yet") : ref.name)
                if !pet && ref.source != nil && ref.source != "faces" {
                    Text("Suggested by an import").font(.footnote).foregroundStyle(.secondary)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// How an AI picture was made, from the evidence in its file: the prompt (to copy), the model and
/// LoRAs (each opening what else was made with it), and the settings.
private struct GenerationSection: View {
    var g: ItemDetails.Generation

    var body: some View {
        Section {
            if let prompt = g.prompt?.trimmingCharacters(in: .whitespacesAndNewlines), !prompt.isEmpty {
                LongText(text: prompt, copy: "Copy the prompt")
                if let n = g.promptUses, n > 1, let id = g.promptId {
                    NavigationLink(value: Route.remote(title: "Same prompt", collection: "prompt:\(id)")) {
                        Text("\(n.formatted()) items use this prompt")
                    }
                }
            }
            if let negative = g.negative?.trimmingCharacters(in: .whitespacesAndNewlines), !negative.isEmpty {
                DisclosureGroup("Negative prompt") { Text(negative).foregroundStyle(.secondary).textSelection(.enabled) }
            }
            if let model = g.model, !model.isEmpty {
                if let id = g.modelId {
                    NavigationLink(value: Route.remote(title: model, collection: "model:\(id)")) { LabeledContent("Model", value: model) }
                } else {
                    LabeledContent("Model", value: model)
                }
            }
            ForEach(g.loras ?? [], id: \.self) { lora in
                let value = lora.name + (lora.weight.map { " · " + $0.formatted(.number.precision(.fractionLength(0...2))) } ?? "")
                if let id = lora.id {
                    NavigationLink(value: Route.remote(title: lora.name, collection: "lora:\(id)")) { LabeledContent("LoRA", value: value) }
                } else {
                    LabeledContent("LoRA", value: value)
                }
            }
            let settings = [g.steps.map { "\($0) steps" }, g.cfg.map { "CFG " + $0.formatted(.number.precision(.fractionLength(0...1))) },
                            g.sampler, g.scheduler, g.seed.map { "seed \($0)" }]
                .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
            if !settings.isEmpty { Text(settings).font(.subheadline.monospacedDigit()).foregroundStyle(.secondary).textSelection(.enabled) }
            Text(DetailText.evidence(g)).font(.footnote).foregroundStyle(.secondary)
        } header: {
            Text(g.kind == "edited" ? "Edited with AI" : "Made with AI")
        }
    }
}

/// Text that may be long (a prompt, the words in a picture): six lines, then More; and a copy button.
private struct LongText: View {
    var text: String
    var copy: String
    @State private var open = false
    @State private var copied = false

    private var long: Bool { text.count > 280 || text.filter { $0 == "\n" }.count > 5 }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(text).lineLimit(open || !long ? nil : 6).textSelection(.enabled)
            HStack(spacing: 20) {
                Button {
                    UIPasteboard.general.string = text
                    copied = true
                    Task {
                        try? await Task.sleep(for: .seconds(1.5))
                        copied = false
                    }
                } label: {
                    Label(copied ? "Copied" : copy, systemImage: copied ? "checkmark" : "doc.on.doc")
                }
                if long { Button(open ? "Less" : "More") { withAnimation { open.toggle() } } }
            }
            .buttonStyle(.borderless) // each its own button, not the row's
            .font(.subheadline.weight(.semibold))
        }
        .padding(.vertical, 2)
        .sensoryFeedback(.success, trigger: copied) { _, now in now }
    }
}

/// Stars, a flag and a color label for one item, set from its details (when Ratings & flags is on).
struct MarksEditor: View {
    var item: Item
    @Environment(Library.self) private var library
    @State private var rating: Int
    @State private var flag: String
    @State private var label: String

    init(item: Item) {
        self.item = item
        _rating = State(initialValue: item.rating)
        _flag = State(initialValue: item.flag)
        _label = State(initialValue: item.label)
    }

    var body: some View {
        Section("Rating and flags") {
            HStack(spacing: 14) {
                ForEach(1...5, id: \.self) { n in
                    Button { set(rating: rating == n ? 0 : n) } label: { Image(systemName: n <= rating ? "star.fill" : "star") }
                        .buttonStyle(.plain)
                        .foregroundStyle(n <= rating ? Color.yellow : Color.secondary)
                        .accessibilityLabel("\(n) star\(n == 1 ? "" : "s")")
                }
                Spacer()
                Menu {
                    Button { set(flag: "pick") } label: { Label("Pick", systemImage: "flag.fill") }
                    Button { set(flag: "reject") } label: { Label("Reject", systemImage: "xmark.circle") }
                    Button("No flag") { set(flag: "none") }
                } label: {
                    Image(systemName: flag == "pick" ? "flag.fill" : flag == "reject" ? "xmark.circle.fill" : "flag")
                }
                .accessibilityLabel("Flag")
                Menu {
                    ForEach(["red", "yellow", "green", "blue", "purple"], id: \.self) { c in
                        Button(c.capitalized) { set(label: c) }
                    }
                    Button("No label") { set(label: "none") }
                } label: {
                    Image(systemName: label.isEmpty ? "circle" : "circle.fill").foregroundStyle(label.isEmpty ? Color.secondary : labelColor(label))
                }
                .accessibilityLabel("Color label")
            }
            .font(.title3)
        }
    }

    private func set(rating r: Int? = nil, flag f: String? = nil, label l: String? = nil) {
        if let r { rating = r }
        if let f { flag = f == "none" ? "" : f }
        if let l { label = l == "none" ? "" : l }
        Task { try? await library.mark([item.id], rating: r, flag: f, label: l) }
    }
}
