// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "ScreenReader",
    platforms: [.macOS(.v13)],
    targets: [
        .target(name: "ScreenReaderKit"),
        .executableTarget(
            name: "ScreenReader",
            dependencies: ["ScreenReaderKit"]
        ),
        .testTarget(
            name: "ScreenReaderKitTests",
            dependencies: ["ScreenReaderKit"]
        ),
    ]
)
