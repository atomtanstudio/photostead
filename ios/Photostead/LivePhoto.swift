// A Live Photo's motion, as the iPhone's Photos plays it: the clip (a video of its own on the
// server, /assets/{motionId}/original) plays over the photo while it's pressed, or once from the
// LIVE badge, and the photo comes back when it ends. The clip loads as soon as its photo is the one
// shown, so a press starts it at once. It's muted unless sound is turned on from the badge's menu;
// even then it's silent with the phone on silent and leaves other apps' audio playing (the ambient
// audio category), as Photos does. With Reduce Motion it comes and goes without a fade.

import AVFoundation
import SwiftUI

/// Whether Live Photos play with their sound (off unless chosen; kept for next time).
let liveSoundKey = "liveSound"

/// The clip over its photo: invisible until it plays, then fading in once its first frame is ready.
struct LiveMotion: UIViewRepresentable {
    var url: URL
    var token: String
    var playing: Bool
    var ended: () -> Void
    @AppStorage(liveSoundKey) private var sound = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func makeCoordinator() -> Clip { Clip(url: url, token: token) }

    func makeUIView(context: Context) -> Clip.Layer {
        let view = Clip.Layer()
        view.player.player = context.coordinator.player
        view.player.videoGravity = .resizeAspect
        view.alpha = 0
        view.isUserInteractionEnabled = false
        view.accessibilityElementsHidden = true
        context.coordinator.attach(view)
        return view
    }

    func updateUIView(_ view: Clip.Layer, context: Context) {
        let clip = context.coordinator
        clip.ended = ended
        clip.fade = !reduceMotion
        clip.player.isMuted = !sound
        if playing != clip.playing { playing ? clip.play() : clip.stop() }
    }

    static func dismantleUIView(_ view: Clip.Layer, coordinator: Clip) {
        coordinator.stop()
        coordinator.player.replaceCurrentItem(with: nil)
    }

    final class Clip {
        let player: AVPlayer
        var playing = false
        var fade = true
        var ended: () -> Void = {}
        private weak var view: Layer?
        private var ready: NSKeyValueObservation?
        private var end: NSObjectProtocol?

        init(url: URL, token: String) {
            let asset = AVURLAsset(url: url, options: ["AVURLAssetHTTPHeaderFieldsKey": ["Authorization": "Bearer \(token)"]])
            let item = AVPlayerItem(asset: asset)
            player = AVPlayer(playerItem: item)
            player.isMuted = true
            player.actionAtItemEnd = .pause
            player.preventsDisplaySleepDuringVideoPlayback = false
            end = NotificationCenter.default.addObserver(forName: AVPlayerItem.didPlayToEndTimeNotification, object: item, queue: .main) { [weak self] _ in
                guard let self, self.playing else { return }
                self.stop()
                self.ended()
            }
        }

        deinit {
            if let end { NotificationCenter.default.removeObserver(end) }
        }

        func attach(_ view: Layer) {
            self.view = view
            ready = view.player.observe(\.isReadyForDisplay) { [weak self] layer, _ in
                DispatchQueue.main.async { if layer.isReadyForDisplay, self?.playing == true { self?.show(true) } }
            }
        }

        func play() {
            playing = true
            Self.audio(on: true)
            player.seek(to: .zero, toleranceBefore: .zero, toleranceAfter: .zero)
            player.play()
            if view?.player.isReadyForDisplay == true { show(true) } // otherwise once its first frame is (see attach)
        }

        func stop() {
            guard playing else { return }
            playing = false
            player.pause()
            show(false)
            Self.audio(on: false)
        }

        private func show(_ on: Bool) {
            guard let view else { return }
            let change = { view.alpha = on ? 1 : 0 }
            if fade { UIView.animate(withDuration: on ? 0.15 : 0.25, delay: 0, options: [.beginFromCurrentState, .allowUserInteraction], animations: change) } else { change() }
        }

        /// Ambient while a clip plays (quiet on silent, mixed with other apps' audio, as Photos plays
        /// them); the app's usual audio otherwise, so its videos play as they did.
        private static func audio(on: Bool) {
            try? AVAudioSession.sharedInstance().setCategory(on ? .ambient : .soloAmbient)
        }

        final class Layer: UIView {
            override class var layerClass: AnyClass { AVPlayerLayer.self }
            var player: AVPlayerLayer { layer as! AVPlayerLayer }
        }
    }
}

/// The viewer's LIVE badge: a tap plays the clip once; holding it offers sound on or off.
struct LiveBadge: View {
    var playing: Bool
    var play: () -> Void
    @AppStorage(liveSoundKey) private var sound = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Menu {
            Button(action: play) { Label("Play", systemImage: "play") }
            Toggle(isOn: $sound) { Label("Sound", systemImage: sound ? "speaker.wave.2" : "speaker.slash") }
        } label: {
            HStack(spacing: 3) {
                Image(systemName: "livephoto").symbolEffect(.pulse, isActive: playing && !reduceMotion)
                Text("LIVE")
            }
            .font(.caption2.weight(.bold))
            .padding(.horizontal, 6).padding(.vertical, 2)
            .background(.white.opacity(playing ? 0.35 : 0.2), in: Capsule())
        } primaryAction: {
            play()
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .accessibilityLabel("Live Photo")
        .accessibilityValue(sound ? "Sound on" : "Muted")
        .accessibilityHint("Plays it. Touch and hold for sound.")
        .accessibilityIdentifier("live")
    }
}
