// swift-tools-version: 6.1

import PackageDescription

let package = Package(
    name: "MeetingAssistApple",
    platforms: [
        .iOS(.v17),
        .macOS(.v14)
    ],
    products: [
        .library(name: "MeetingAssistApple", targets: ["MeetingAssistApple"]),
        .library(name: "MeetingAssistMac", targets: ["MeetingAssistMac"]),
        .library(name: "MeetingAssistCore", targets: ["MeetingAssistCore"]),
        .library(name: "MeetingAssistAPI", targets: ["MeetingAssistAPI"]),
        .library(name: "MeetingAssistSignaling", targets: ["MeetingAssistSignaling"]),
        .library(name: "MeetingAssistRoomRTC", targets: ["MeetingAssistRoomRTC"]),
        .library(name: "MeetingAssistMedia", targets: ["MeetingAssistMedia"]),
        .library(name: "MeetingAssistRoom", targets: ["MeetingAssistRoom"]),
        .library(name: "MeetingAssistScout", targets: ["MeetingAssistScout"]),
        .library(name: "MeetingAssistDesign", targets: ["MeetingAssistDesign"])
    ],
    dependencies: [],
    targets: [
        .target(name: "MeetingAssistCore"),
        .target(name: "MeetingAssistAPI", dependencies: ["MeetingAssistCore"]),
        .target(name: "MeetingAssistSignaling", dependencies: ["MeetingAssistCore"]),
        .target(name: "MeetingAssistRoomRTC", dependencies: ["MeetingAssistCore"]),
        .target(name: "MeetingAssistMedia", dependencies: ["MeetingAssistCore", "MeetingAssistRoomRTC"]),
        .target(
            name: "MeetingAssistRoom",
            dependencies: [
                "MeetingAssistCore",
                "MeetingAssistAPI",
                "MeetingAssistSignaling",
                "MeetingAssistRoomRTC",
                "MeetingAssistMedia"
            ]
        ),
        .target(name: "MeetingAssistScout", dependencies: ["MeetingAssistCore", "MeetingAssistAPI"]),
        .target(name: "MeetingAssistDesign", dependencies: ["MeetingAssistCore"]),
        .target(
            name: "MeetingAssistApple",
            dependencies: [
                "MeetingAssistCore",
                "MeetingAssistAPI",
                "MeetingAssistSignaling",
                "MeetingAssistRoomRTC",
                "MeetingAssistMedia",
                "MeetingAssistRoom",
                "MeetingAssistScout",
                "MeetingAssistDesign"
            ],
            path: "Apps/MeetingAssistIOS"
        ),
        .target(
            name: "MeetingAssistMac",
            dependencies: [
                "MeetingAssistCore",
                "MeetingAssistAPI",
                "MeetingAssistSignaling",
                "MeetingAssistRoomRTC",
                "MeetingAssistMedia",
                "MeetingAssistRoom",
                "MeetingAssistScout",
                "MeetingAssistDesign"
            ],
            path: "Apps/MeetingAssistMac"
        ),
        .testTarget(name: "MeetingAssistCoreTests", dependencies: ["MeetingAssistCore"]),
        .testTarget(name: "MeetingAssistAPITests", dependencies: ["MeetingAssistAPI", "MeetingAssistCore"]),
        .testTarget(name: "MeetingAssistSignalingTests", dependencies: ["MeetingAssistSignaling", "MeetingAssistCore"]),
        .testTarget(
            name: "MeetingAssistRoomTests",
            dependencies: [
                "MeetingAssistRoom",
                "MeetingAssistCore",
                "MeetingAssistRoomRTC"
            ]
        )
    ]
)
