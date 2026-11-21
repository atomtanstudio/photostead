// The first run, once, after pairing: why Photostead needs Photos (before iOS asks), how backup goes
// on by itself, and backup notices, if you want them. Two pages; one when Photos is settled already
// (paired before, or allowed). Nothing is asked for until you tap for it. The Photos page only goes
// on, to iOS's own question (where you can say no): App Review turns away a page before a
// permission request that offers a way round it.

import Photos
import SwiftUI

struct Welcome: View {
    @Environment(Backup.self) private var backup
    @Environment(\.openURL) private var openURL
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var pages: [Page]
    @State private var at = 0
    @State private var asking = false
    private let notices = Notices.shared

    enum Page { case photos, backup }

    init() {
        let access = PHPhotoLibrary.authorizationStatus(for: .readWrite)
        _pages = State(initialValue: access == .authorized || access == .limited ? [.backup] : [.photos, .backup])
    }

    var body: some View {
        ZStack {
            switch pages[at] {
            case .photos: photos.transition(turn)
            case .backup: backingUp.transition(turn)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .modifier(BottomBar { footer })
        .background(Color(.systemBackground))
    }

    private var denied: Bool { backup.access == .denied || backup.access == .restricted }

    // MARK: Pages

    @ViewBuilder private var photos: some View {
        if denied {
            WelcomePage(symbol: "photo.on.rectangle.angled", title: "Allow Photos in Settings",
                 text: "Photostead can’t see your Photos library, so it can’t back it up. Allow it in Settings → Photostead → Photos. You can still browse your library.") {}
        } else {
            WelcomePage(symbol: "photo.on.rectangle.angled", title: "Back up your photos",
                 text: "Photostead needs to see your Photos library to back it up. When iOS asks, choose Allow Full Access, so it can back up everything, new photos too.") {
                Point(symbol: "house", title: "To your own server", text: "Your photos and videos go to your Photostead, and nowhere else.")
                Point(symbol: "photo.stack", title: "The originals", text: "At full quality, with Live Photos and videos.")
            }
        }
    }

    private var backingUp: some View {
        WelcomePage(symbol: "arrow.up.circle", title: "Backup looks after itself", text: "You don’t need to keep Photostead open.") {
            Point(symbol: "clock.arrow.circlepath", title: "Newest first",
                  text: "Your latest photos and videos go first, and only what Photostead doesn’t have yet.")
            Point(symbol: "battery.100percent.bolt", title: "In the background",
                  text: "It carries on after you leave the app, and iOS sends some by itself, mostly while your iPhone is charging.")
            Point(symbol: "wifi", title: "On Wi-Fi", text: "Unless you allow cellular data in Settings.")
            notify.padding(.top, 8)
        }
    }

    /// Optional, and asked for in context: turning it on is what asks iOS.
    private var notify: some View {
        VStack(alignment: .leading, spacing: 10) {
            Toggle(isOn: Binding(get: { notices.on }, set: { want in Task { await notices.turn(want) } })) {
                Label {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Tell me when backups finish or get stuck")
                        Text("One notification an hour, at most.").font(.footnote).foregroundStyle(.secondary)
                    }
                } icon: {
                    Image(systemName: "bell.badge")
                }
            }
            .accessibilityIdentifier("welcome-notify")
            if notices.on && notices.blocked {
                Button("Notifications are off for Photostead. Allow them in Settings.") {
                    openURL(URL(string: UIApplication.openNotificationSettingsURLString)!)
                }
                .font(.footnote)
            }
        }
        .padding(16)
        .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 22, style: .continuous))
    }

    // MARK: Going on

    private var footer: some View {
        VStack(spacing: 14) {
            if pages.count > 1 {
                Dots(count: pages.count, at: at)
            }
            Button(action: primary) {
                Text(primaryTitle).frame(maxWidth: .infinity).foregroundStyle(Color.onAccent)
            }
            .buttonStyle(.borderedProminent).controlSize(.large)
            .disabled(asking)
            .accessibilityIdentifier(pages[at] == .backup ? "welcome-done" : "welcome-continue")
            if pages[at] == .photos && denied {
                Button("Not now", action: next).controlSize(.large)
            }
        }
        .padding(.horizontal, 24).padding(.top, 12).padding(.bottom, 16)
        .frame(maxWidth: 560)
    }

    private var primaryTitle: String {
        switch pages[at] {
        case .photos: denied ? "Open Settings" : "Continue"
        case .backup: "Done"
        }
    }

    private func primary() {
        switch pages[at] {
        case .photos where denied:
            openURL(URL(string: UIApplication.openSettingsURLString)!)
            next()
        case .photos:
            asking = true
            Task {
                await backup.requestAccess() // iOS's question; backing up starts once it's answered yes
                asking = false
                next()
            }
        case .backup:
            backup.welcoming = false
        }
    }

    private func next() {
        guard at + 1 < pages.count else {
            backup.welcoming = false
            return
        }
        withAnimation(reduceMotion ? .easeInOut(duration: 0.2) : .smooth) { at += 1 }
    }

    private var turn: AnyTransition {
        reduceMotion ? .opacity : .asymmetric(insertion: .move(edge: .trailing).combined(with: .opacity),
                                              removal: .move(edge: .leading).combined(with: .opacity))
    }
}

/// One page: a symbol, a title, a line, and what it says in points.
private struct WelcomePage<Content: View>: View {
    var symbol: String
    var title: String
    var text: String
    @ViewBuilder var content: Content

    var body: some View {
        ScrollView {
            VStack(spacing: 28) {
                Image(systemName: symbol)
                    .font(.system(size: 40))
                    .frame(width: 92, height: 92)
                    .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 24, style: .continuous))
                    .accessibilityHidden(true)
                VStack(spacing: 12) {
                    Text(title).font(.largeTitle.weight(.bold)).accessibilityAddTraits(.isHeader)
                    Text(text).foregroundStyle(.secondary)
                }
                .multilineTextAlignment(.center)
                VStack(alignment: .leading, spacing: 22) { content }
            }
            .padding(.horizontal, 28).padding(.top, 44).padding(.bottom, 24)
            .frame(maxWidth: 560)
            .frame(maxWidth: .infinity)
        }
        .scrollBounceBehavior(.basedOnSize)
    }
}

/// A symbol and a short paragraph; stacked at the largest text sizes, where side by side squeezes it.
private struct Point: View {
    var symbol: String
    var title: String
    var text: String
    @Environment(\.dynamicTypeSize) private var size

    var body: some View {
        let layout = size.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 8)) : AnyLayout(HStackLayout(alignment: .top, spacing: 16))
        layout {
            Image(systemName: symbol).font(.title2).frame(width: 34)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.headline)
                Text(text).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
    }
}

/// The buttons, at the bottom, with a page scrolling under them (at the largest text sizes): softened
/// beneath them, as iOS does under its own bars.
private struct BottomBar<Bar: View>: ViewModifier {
    @ViewBuilder var bar: Bar

    func body(content: Content) -> some View {
        if #available(iOS 26.0, *) {
            content.safeAreaBar(edge: .bottom) { bar }
        } else {
            content.safeAreaInset(edge: .bottom) { bar.frame(maxWidth: .infinity).background(.bar) }
        }
    }
}

/// Where you are: one dot a page.
private struct Dots: View {
    var count: Int
    var at: Int

    var body: some View {
        HStack(spacing: 8) {
            ForEach(0..<count, id: \.self) { i in
                Circle().fill(i == at ? AnyShapeStyle(.primary) : AnyShapeStyle(.tertiary)).frame(width: 7, height: 7)
            }
        }
        .accessibilityElement()
        .accessibilityLabel("Page \(at + 1) of \(count)")
    }
}
