// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "StateDesktop",
    platforms: [.macOS(.v14)],
    products: [.executable(name: "StateServerMac", targets: ["StateServerMac"])],
    targets: [
        .target(name: "StateLocalTransport", path: "shared/LocalTransport"),
        .executableTarget(name: "StateServerMac", dependencies: ["StateLocalTransport"], path: "macos/Sources"),
        .testTarget(name: "StateLocalTransportTests", dependencies: ["StateLocalTransport"], path: "macos/Tests"),
    ]
)
