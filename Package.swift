// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "StateDesktop",
    platforms: [.macOS(.v14)],
    products: [.executable(name: "StateServerMac", targets: ["StateServerMac"])],
    targets: [
        .target(name: "StateLocalTransport", path: "shared/LocalTransport"),
        .target(name: "StateServerCore", path: "macos/Core"),
        .executableTarget(name: "StateServerMac", dependencies: ["StateLocalTransport", "StateServerCore"], path: "macos/Sources"),
        .testTarget(name: "StateLocalTransportTests", dependencies: ["StateLocalTransport"], path: "macos/Tests"),
        .testTarget(name: "StateServerCoreTests", dependencies: ["StateServerCore"], path: "macos/CoreTests"),
    ]
)
