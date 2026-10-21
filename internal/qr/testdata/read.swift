// Read the QR code in each PNG given, and print its text (one line per file; NONE when unreadable).
// Used by qr_test.go on macOS, through CoreImage, as an independent reader.
import CoreImage
import Foundation

let detector = CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh])!
for path in CommandLine.arguments.dropFirst() {
    guard let image = CIImage(contentsOf: URL(fileURLWithPath: path)) else {
        print("NONE")
        continue
    }
    let features = detector.features(in: image).compactMap { $0 as? CIQRCodeFeature }
    print(features.first?.messageString ?? "NONE")
}
