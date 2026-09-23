# Product

<!-- impeccable:product-schema 1 -->

## Platform

ios

## Users

Primary user for now: the owner himself, a developer who runs several coding agents (Claude Code, Codex, OpenCode, Pi, DeepSeek Harness) every day and hosts his own State server. He opens State on the iPhone to see what is due and on the Mac while he works. He is technical and does not need server concepts explained twice, but he expects the app to feel like a first-party Apple app.

## Product Purpose

State is the durable memory and reminder system for coding agents. Agents write explicit tasks and recurring obligations into State during normal work; the owner sees, completes and snoozes them on iPhone, iPad and Mac, and due tasks can run as agent sessions through a local runner. Success: nothing an agent was told to remember gets lost, and the owner trusts the app enough to rely on it daily.

## Positioning

A self-hosted, audited reminder inbox that every agent harness shares through MCP, with the owner's phone as the place of record. Data stays on the owner's own server (Mac Server or VPS).

## Operating Context

- First run: connect the app to a server with a server address plus either a bootstrap token (first owner device) or a one-time pairing code, or scan a pairing QR code shown by the Mac Server app. The Mac has no scanner and pastes a pairing link instead.
- Daily use: Today and Planned lists, reminder detail with comments and agent runs, activity history, settings.
- iPhone uses tabs; iPad and Mac use a three-column split layout.

## Capabilities and Constraints

- Native SwiftUI, iOS 18 and macOS 15, one shared code base, German and English.
- Onboarding must keep: server address, bootstrap token or pairing code, owner name, device name, QR scan (iOS), pairing link paste (macOS), demo entry, link to setup documentation.
- The Mac app is sandboxed; it gets no APNs pushes.

## Brand Commitments

- Name: State.
- Icon: the black glossy "S" mark with a yellow dot on a pale blue-grey ground (taken over from The System app on 23.09.2026).
- Direction confirmed by the owner: "mini-minimal". Premium, first-party iPhone quality, uncluttered.

## Evidence on Hand

- Icon source: `~/Desktop/the-system-app/ios/Runner/Assets.xcassets/AppIcon.appiconset/Icon-App-1024x1024@1x.png`.
- No testimonials, metrics or marketing claims exist; none may be invented.

## Product Principles

1. One thing per screen: the first run asks only for what is needed right now.
2. Native first: system controls, system typography, platform conventions on iPhone and Mac.
3. Quiet confidence: the owner's data and credentials stay his; say it once, calmly.
4. Nothing hidden that is needed, nothing shown that is not.
