import SwiftUI

/// Таймлайн плеера: дорожка 3 pt, буфер, сегменты скипов, зелёное проигранное,
/// ползунок 8 pt; тап — перейти, драг — скраб с подтверждением на отпускании.
struct SeekBar: View {
    let position: Double
    let duration: Double
    let buffered: Double
    let segments: [SkipSegment]
    var trackColor: Color
    var bufferedColor: Color
    var playedColor: Color
    var segColor: Color
    var onCommit: (Double) -> Void

    @State private var dragPos: Double?
    @GestureState private var dragging = false

    var body: some View {
        GeometryReader { geo in
            let w = geo.size.width
            let dur = max(duration, 0.001)
            let pos = dragPos ?? position
            ZStack(alignment: .leading) {
                Capsule().fill(trackColor).frame(height: 3)
                Capsule().fill(bufferedColor)
                    .frame(width: w * min(buffered / dur, 1), height: 3)
                ForEach(segments, id: \.start) { seg in
                    Capsule().fill(segColor)
                        .frame(width: max(w * (seg.end - seg.start) / dur, 3), height: 3)
                        .offset(x: w * seg.start / dur)
                }
                Capsule().fill(playedColor)
                    .frame(width: w * min(pos / dur, 1), height: 3)
                Circle()
                    .fill(playedColor)
                    .frame(width: dragging ? 10 : 8, height: dragging ? 10 : 8)
                    .offset(x: w * min(pos / dur, 1) - (dragging ? 5 : 4))
            }
            .frame(height: 32, alignment: .center)
            .contentShape(Rectangle())
            .gesture(
                DragGesture(minimumDistance: 0)
                    .updating($dragging) { _, state, _ in state = true }
                    .onChanged { value in
                        dragPos = min(max(0, Double(value.location.x / w) * dur), dur)
                    }
                    .onEnded { value in
                        let target = min(max(0, Double(value.location.x / w) * dur), dur)
                        dragPos = nil
                        onCommit(target)
                    }
            )
        }
        .frame(height: 32)
    }
}
