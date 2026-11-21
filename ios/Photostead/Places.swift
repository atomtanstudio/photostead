// Places: where items were taken, named offline by the server, as a list by country (as the web
// app has them), each opening its items. And a map, only when the server shows one: its owner may
// have turned the web's map off (PHOTOSTEAD_MAP=off) so no tile server sees where they look. The
// iPhone's map is Apple's (MapKit), which sees the same thing the web's tile server would, so it
// follows the same setting, and it waits to be asked for: Places opens as the list.

import MapKit
import SwiftUI

struct PlacesView: View {
    @AppStorage("placesOnMap") private var onMap = false
    @State private var loaded = false

    private var browse: Browse { Browse.shared }
    private var showMap: Bool { onMap && browse.map && !browse.places.isEmpty }

    var body: some View {
        Group {
            if showMap {
                PlacesMap(places: browse.places)
            } else {
                list
            }
        }
        .navigationTitle("Places")
        .navigationBarTitleDisplayMode(showMap ? .inline : .automatic) // a large title would sit over the map
        .toolbar {
            if browse.map && !browse.places.isEmpty {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { onMap.toggle() } label: {
                        Label(onMap ? "Show as a list" : "Show on a map", systemImage: onMap ? "list.bullet" : "map")
                    }
                    .accessibilityIdentifier("places-map")
                }
            }
        }
        .task {
            await browse.refresh(force: true)
            loaded = true
        }
    }

    private var list: some View {
        List {
            if browse.places.isEmpty {
                if loaded {
                    ContentUnavailableView("No places yet", systemImage: "mappin.and.ellipse",
                                           description: Text("Photos and videos that record where they were taken show up here."))
                        .listRowBackground(Color.clear)
                } else {
                    ProgressView().frame(maxWidth: .infinity).listRowBackground(Color.clear)
                }
            }
            ForEach(placesByCountry(browse.places), id: \.country) { group in
                Section(group.country) {
                    ForEach(group.places) { place in
                        NavigationLink(value: Route.place(place)) { PlaceLine(place: place) }
                            .accessibilityIdentifier("place")
                    }
                }
            }
            if !browse.places.isEmpty {
                Section {} footer: { Text(browse.attribution) }
            }
        }
        .refreshable { await browse.refresh(force: true) }
    }
}

/// A place in the list: its newest item, its name, and the region and how many.
private struct PlaceLine: View {
    var place: PlaceRow

    var body: some View {
        HStack(spacing: 14) {
            CoverPicture(id: place.cover, hash: place.coverThumbhash, symbol: "mappin")
                .frame(width: 56, height: 56)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            VStack(alignment: .leading, spacing: 2) {
                Text(place.name).font(.body.weight(.medium))
                Text(place.line).font(.subheadline).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .combine)
    }
}

/// Where the map opens: around the place with the most pictures within a few hundred kilometres
/// (home, for most people), framing those places, not every place at once. Fitting them all, a
/// few trips abroad left a map of ocean with home's pins on top of one another; the trips are a
/// pinch away. Nil without places.
func placesMapFrame(_ places: [PlaceRow]) -> MKCoordinateRegion? {
    let reach = 300.0 // km: what counts as near
    func km(_ a: PlaceRow, _ b: PlaceRow) -> Double { // near enough for a few hundred km, and cheap
        var dLon = abs(a.lon - b.lon)
        if dLon > 180 { dLon = 360 - dLon }
        let x = dLon * cos((a.lat + b.lat) / 2 * .pi / 180) * 111.32, y = (a.lat - b.lat) * 110.57
        return (x * x + y * y).squareRoot()
    }
    // The busiest places are the candidates, so a library of thousands of places stays quick.
    let candidates = places.sorted { $0.count > $1.count }.prefix(200)
    var best: (home: PlaceRow, near: [PlaceRow], count: Int)?
    for c in candidates {
        let near = places.filter { km($0, c) <= reach }
        let count = near.reduce(0) { $0 + $1.count }
        if count > best?.count ?? -1 { best = (c, near, count) }
    }
    guard let best else { return nil }
    // Longitudes as seen from home, so places across the date line from it stay beside it.
    let wrap = { (lon: Double) in (lon + 540).truncatingRemainder(dividingBy: 360) - 180 }
    let lats = best.near.map(\.lat), lons = best.near.map { best.home.lon + wrap($0.lon - best.home.lon) }
    let (south, north, west, east) = (lats.min()!, lats.max()!, lons.min()!, lons.max()!)
    // A little wider than the places, so the pins at the edges show whole; one place alone
    // opens on its surroundings, not its streets.
    return MKCoordinateRegion(center: CLLocationCoordinate2D(latitude: (south + north) / 2, longitude: wrap((west + east) / 2)),
                              span: MKCoordinateSpan(latitudeDelta: min(max((north - south) * 1.5, 0.3), 170),
                                                     longitudeDelta: min(max((east - west) * 1.5, 0.3), 350)))
}

/// Each place on Apple's map at its town, as its newest item; tapping one opens its items.
private struct PlacesMap: View {
    var places: [PlaceRow]

    var body: some View {
        Map(initialPosition: placesMapFrame(places).map { .region($0) } ?? .automatic) {
            ForEach(places) { place in
                Annotation(place.name, coordinate: CLLocationCoordinate2D(latitude: place.lat, longitude: place.lon), anchor: .bottom) {
                    NavigationLink(value: Route.place(place)) { Pin(place: place) }
                        .buttonStyle(.plain)
                        .accessibilityLabel("\(place.name), \(place.line)")
                        .accessibilityIdentifier("place-pin")
                }
            }
        }
        .mapStyle(.standard(pointsOfInterest: .excludingAll))
        .ignoresSafeArea(edges: .bottom)
    }

    private struct Pin: View {
        var place: PlaceRow

        var body: some View {
            CoverPicture(id: place.cover, hash: place.coverThumbhash, symbol: "mappin")
                .frame(width: 52, height: 52)
                .clipShape(RoundedRectangle(cornerRadius: 12))
                .overlay { RoundedRectangle(cornerRadius: 12).stroke(.background, lineWidth: 3) }
                .shadow(color: .black.opacity(0.25), radius: 4, y: 2)
                .overlay(alignment: .topTrailing) {
                    Text(place.count > 999 ? "\((Double(place.count) / 1000).formatted(.number.precision(.fractionLength(1))))k" : "\(place.count)")
                        .font(.caption2.weight(.semibold).monospacedDigit())
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background(.regularMaterial, in: Capsule())
                        .offset(x: 8, y: -8)
                }
        }
    }
}

/// An item's thumbnail by its id and ThumbHash (a place's, a character's, a prompt's), or a symbol
/// when there's none.
struct CoverPicture: View {
    var id: String?
    var hash: Data?
    var symbol = "photo"

    var body: some View {
        Color.clear
            .overlay {
                if let id {
                    ItemPicture(item: Item(id: id, type: "image", takenAt: .distantPast, localDate: "", width: 0, height: 0, durationMs: 0,
                                           thumbhash: hash, motionID: nil, favorite: false, fileName: "", ai: ""))
                } else {
                    Rectangle().fill(.quaternary).overlay { Image(systemName: symbol).foregroundStyle(.secondary) }
                }
            }
            .clipped()
    }
}
