# iOS release checklist

This checklist separates repository preparation from the App Store Connect decisions that must be made by the account holder. It applies to `com.fabincrm.state`, version 1.0.0.

## Prepared in the repository

- App Store name, bundle identifier, SKU and Team ID are configured.
- German and English metadata, support URL, privacy URL, review notes and 6.9-inch screenshots are present.
- The primary category is Productivity and the copyright notice is included in Fastlane metadata.
- The main app and Notification Service Extension declare their bundle identifiers, App Group and release entitlements.
- The archive lane uses automatic provisioning and the beta lane targets internal TestFlight distribution.
- The build number never touches a tracked file. `ios/project.yml` keeps `CURRENT_PROJECT_VERSION: 1`, both `Info.plist` files keep `$(CURRENT_PROJECT_VERSION)`, and `fastlane ios build` passes the real value through `xcargs`. Override it with `BUILD_NUMBER=...`, otherwise a UTC `yymmddHHMM` stamp is used. That format stays below the App Store Connect limit of 2^32 per version component.
- The app declares `ITSAppUsesNonExemptEncryption` as false. The account holder must confirm that export classification before App Store submission.

## Credentials

This repository is public and open source, so it carries no Apple account of any kind. Every account specific value comes from `ios/fastlane/.env`, which fastlane loads automatically and git ignores. Copy `ios/fastlane/.env.example` and fill it in:

```bash
cp ios/fastlane/.env.example ios/fastlane/.env
```

A lane that needs a value you have not set stops with the variable name and what it is for, rather than failing somewhere inside Apple's API. Anyone forking State supplies their own account here; nothing is shared.

## The one step that needs an Apple ID

The App Store Connect API cannot create an app record. Asked to, it answers `The resource 'apps' does not allow 'CREATE'. Allowed operations are: GET_COLLECTION, GET_INSTANCE, UPDATE`. Only the App Store Connect web session can, so `create_app` falls back to `produce`, which needs an Apple ID and a two-factor confirmation:

```bash
cd ios
FASTLANE_USER=your-apple-id bundle exec fastlane ios create_app
```

Run it from a terminal that can answer the prompt. If the record already exists the lane detects that through the API key alone and does nothing. Every other lane runs on the API key with no interactive login.

## App Store Connect decisions

1. Age Rating: automated. `ios/fastlane/age_rating.json` holds the answers and `fastlane ios metadata` uploads them through `deliver`. Every content category is `NONE`, and messaging, user generated content, advertising and unrestricted web access are all false, because State shows only the owner's own reminders from the owner's own server. Revisit the file if that stops being true.
2. App Privacy: `ios/fastlane/app_privacy_details.json` is the reviewed declaration and `fastlane ios privacy` validates it, but no fastlane action uploads the privacy nutrition label. Enter it by hand from that file and keep the two in step. The draft declares reminder content, identifiers and the owner's chosen display name, all linked to the user, all for app functionality, no tracking. The account holder still has to confirm how a self-hosted server and the optional shared relay should be represented.
3. Pricing: `fastlane ios pricing` sets State to free with Germany as the base territory and does nothing if a free price is already active.
4. Availability: still manual. Which storefronts State is sold in is a product and legal decision, not an automation default.
5. Export Compliance: `submission_info_defaults` in the Fastfile now answers the whole questionnaire consistently with `ITSAppUsesNonExemptEncryption=false` in `Info.plist`. Before submitting, confirm that answer is right: State does more than plain HTTPS, it builds its own encrypted push envelopes and signs the audit chain, even though it does so through Apple's CryptoKit. If that turns out to need a different answer, change the plist and `submission_info_defaults` together, never one alone.
6. App Review contact: App Store Connect requires a first name, last name, email and phone number as soon as a build is attached, and rejects the whole metadata upload without them. `deliver` sends them from `app_review_information` in the Fastfile. The name and email default to values already public in this repository's history; the phone number is deliberately not stored here because the repository is public. Put it in `ios/fastlane/.env` as `ASC_REVIEW_PHONE`:

```bash
cd ios
bundle exec fastlane ios metadata
```

## Verifying the listing

`bundle exec fastlane ios audit` reads every field back out of App Store Connect rather than trusting an upload log. Two faults already shipped past a green log: a placeholder App Store icon because no build was selected for the version, and duplicated screenshots. Run it before every submission.

It checks the version state and its build, the categories, the app info localizations, per locale metadata and screenshot counts against the files on disk, pricing, territory availability, the App Review contact and the TestFlight groups with their tester counts.

Two things it cannot check, and says so instead of assuming: the App Privacy questionnaire has no App Store Connect API at all and has to be entered by hand from `ios/fastlane/app_privacy_details.json`, and export compliance is answered at submission time.

## Store assets

Screenshots live in `ios/fastlane/screenshots/{de-DE,en-US}` and cover both device classes App Review needs while `TARGETED_DEVICE_FAMILY` is `1,2`:

- iPhone 6.9 inch at 1320x2868, captured on a 6.9 inch simulator and composed into marketing frames with Higgsfield image to image.
- iPad 13 inch at 2064x2752, captured on an iPad Pro 13 inch simulator and composed by `ios/fastlane/compose_ipad_frames.py`.

Both sets show the untouched app capture inside the frame. Only the backdrop, the device shell and the headline are added, so the screenshots still represent what the app does. Regenerate the iPad set with `python3 ios/fastlane/compose_ipad_frames.py` after changing the captures.

The App Store icon comes from the uploaded build, not from a separate upload. It stays a placeholder in App Store Connect until the first build finishes processing.

## Mac and iPad

iPhone, iPad and Mac are one app: the same project, the same bundle identifier `com.fabincrm.state` and the same version. The Mac app is a native macOS target (`StateMac` in `ios/project.yml`), not Catalyst and not "Designed for iPad", so one app record covers all three through Universal Purchase.

### App Store Connect setup for macOS

1. Add the macOS platform to the existing app record. The bundle identifier stays `com.fabincrm.state` and nothing about the app identity changes.
2. App Store Connect then wants its own version record, its own screenshots and its own metadata for that platform. The existing lanes only ever touch the iOS record: `editable_app_store_version!` in the Fastfile filters on `Platform::IOS`, and `deliver` defaults to `platform: "ios"`.
3. Signing needs a Mac App Store distribution certificate, a Mac Installer Distribution certificate for the `.pkg`, and a Mac App Store provisioning profile for `com.fabincrm.state`. `fastlane mac_build` archives with `-allowProvisioningUpdates`, so Xcode creates or repairs what is missing once an Apple ID that can manage the app is signed in, exactly as it did for iOS.
4. The Mac app is sandboxed (`com.apple.security.app-sandbox` with `com.apple.security.network.client`) and uses the keychain group `com.fabincrm.state.shared`. The Mac App Store profile has to carry that keychain group, otherwise the app cannot read the server credentials it shares with the iPhone.

### Building and uploading

```bash
cd ios
bundle exec fastlane mac_beta    # signed pkg for macOS plus the TestFlight upload
bundle exec fastlane beta        # iPhone and iPad build plus the TestFlight upload
```

Both lanes take the build number from the same source: `BUILD_NUMBER` if it is set, a UTC `yymmddHHMM` stamp otherwise. Override it when a number is already taken, because App Store Connect rejects an upload with a build number that exists for that version and platform.

`bundle exec fastlane mac_test` builds `StateMac` without signing. It is the smoke test for the Mac target, and it needs no Apple account.

### Mac screenshots

fastlane `snapshot` has no macOS support, so `ios/fastlane/Snapfile` covers the iPhone and the iPad only and the Mac set is captured by hand:

1. Build and start the Mac app. `bundle exec fastlane mac_test` leaves it in `ios/build/DerivedData-mac/Build/Products/Debug/State.app`, so `open ios/build/DerivedData-mac/Build/Products/Debug/State.app` starts that build.
2. On the connection screen click "Look around without a server". There is no launch argument for the demo mode: the UI test uses `-stateUITesting` to skip the introduction and then taps the same `explore-demo` button, so the Mac window needs that one click.
3. Resize the window to a 16:10 content size. App Store Connect accepts 1280x800, 1440x900, 2560x1600 and 2880x1800. Use 2880x1800 on a Retina display, which is 1440x900 points.
4. Capture the window with `screencapture -o -w <file>` and click it, or `screencapture -o -l <window id> <file>` when the window id is known.
5. Capture the same four screens as the iPhone set (Today, Planned, Activity, Settings) and store them as `ios/fastlane/screenshots/{de-DE,en-US}/Mac-01-today.png` and so on. `deliver` picks the display type from the pixel size, and 2880x1800 is the Mac set, so the file name only has to stay unique per locale.
6. Upload the Mac set to the macOS version record. `bundle exec fastlane ios metadata` does not do it, for the reason given above. Either add the images in App Store Connect by hand or add a lane that passes `platform: "osx"` to `deliver`.

### Points to check before uploading

1. iPad layout: the sidebar replaces the tabs, and Today, Planned, Activity and Settings are all reachable from it. The iPad build inside `fastlane test` only proves that the layout compiles, not that it is usable.
2. Mac menu commands: `File > New Reminder` (⌘N) and `File > Sync Now` (⌘R) exist and do what the iOS buttons do.
3. Mac without push: expected, not a fault. Only iPhone and iPad register for APNs. The Mac app works with local notifications from the synced data and with sync polling while it runs.
4. Runner command: the Mac app shows the pairing code and the install command for `state-runner` and both can be copied. The runner runs as its own LaunchAgent outside the sandboxed app, so the command has to be usable as shown.
5. Export compliance: the macOS `Info.plist` declares `ITSAppUsesNonExemptEncryption=false`, the same answer as the iOS side and as `submission_info_defaults`. Confirm it stays that way, because State builds its own encrypted push envelopes and signs the audit chain.
6. App Privacy: the declaration in `ios/fastlane/app_privacy_details.json` belongs to the app record, so macOS inherits it. Check that the Mac app adds no data collection of its own. It has no push token and no App Attest, and its network traffic goes to the owner's own server.

## Developer portal and signing

`bundle exec fastlane ios build` archives with `-allowProvisioningUpdates`, so Xcode creates the missing developer portal resources itself. A verified run on August 15, 2026 produced a signed App Store IPA and resolved all of the following:

1. App ID `com.fabincrm.state` with Push Notifications, App Attest, Time Sensitive Notifications and the keychain group.
2. App ID `com.fabincrm.state.notificationservice`.
3. App group `group.com.fabincrm.state` on both App IDs.
4. App Store provisioning profiles for both bundle identifiers.
5. Signing identity `Apple Distribution: Fabian Bitzer (5DKU7FFK4X)`.

The exported entitlements were `aps-environment: production`, `com.apple.developer.devicecheck.appattest-environment: production`, `com.apple.developer.usernotifications.time-sensitive: true`, `com.apple.security.application-groups: group.com.fabincrm.state`, `beta-reports-active: true` and `get-task-allow: false`.

Time Sensitive Notifications needs no Apple approval. Critical Alerts does. If reminders should ever pierce Do Not Disturb entirely, request that entitlement first and only then add `com.apple.developer.usernotifications.critical-alerts` and the `.criticalAlert` authorization option, otherwise signing fails.

If the export fails with `Copy failed`, check `rsync`. Xcode packages the IPA with `/usr/bin/rsync`, which is openrsync, and openrsync starts its server process through `PATH`. A Homebrew rsync 3.x that shadows it aborts with `--extended-attributes: unknown option`. The build lane already pins Apple's rsync for the duration of the archive.

## TestFlight and device verification

1. Sign in to Xcode with the Apple Developer account and run `bundle exec fastlane ios build`. Automatic provisioning creates or verifies the two App IDs, capabilities, App Group assignments and profiles.
2. Run `FASTLANE_USER=your-apple-id bundle exec fastlane ios create_app`, then `bundle exec fastlane ios asc_status`. This creates the App Store Connect record after the main App ID exists. The App Store Connect API cannot create an app record, so `create_app` needs an interactive Apple ID sign-in including two-factor confirmation. Run it from a terminal that can answer that prompt.
3. Run `bundle exec fastlane ios metadata` to upload the localized listing and screenshots.
4. Run `bundle exec fastlane ios beta` to upload an internal build.
5. Run `bundle exec fastlane ios builds`. It reports the processing state and the TestFlight groups. A build only becomes installable at `VALID`, and with no group at all nobody can install it however valid it is, so both lines have to look right before anyone reaches for a phone.
6. Add an internal TestFlight group in App Store Connect and put Fabian in it. Internal groups take App Store Connect users, and the App Store Connect API does not create them, so this is done in the web interface.
7. Install that exact TestFlight build on a physical iPhone.
8. Pair the iPhone with the production State server, create a reminder through MCP, edit it offline in the app, sync, and verify the full activity history.
9. Verify local notification scheduling while the VPS is unavailable. Verify APNs only after production APNs credentials and the permanent relay domain are enabled.

## Before public App Review

- Replace the temporary `sslip.io` endpoints with the permanent production domains.
- Enable production APNs credentials and turn off relay dry-run mode.
- Complete a physical-device accessibility pass for German and English, Dark Mode, Dynamic Type and VoiceOver.
- Enable HSTS only after successful owner pairing and the final TLS smoke test.
- Run and document an encrypted backup restore test for the deployed server.

### One-time Mac setup (done on 23.09.2026)

- The macOS target uses automatic signing with the `Apple Development` identity for every configuration. Do not pin `Apple Distribution` for Release: automatic signing picks the distribution certificate at export time, and a pinned identity fails the archive with "conflicting provisioning settings".
- Automatic signing needs at least one registered Mac in the team before it can create a Mac development profile. Register the build Mac once with `register_device(platform: "mac")` using its Provisioning UDID from `system_profiler SPHardwareDataType`.
- App Store Connect accepts a macOS package only after the app has a macOS platform. Adding a macOS App Store version (1.0.0) creates it.
- After that, `fastlane mac_beta` uploads an internal TestFlight build.
