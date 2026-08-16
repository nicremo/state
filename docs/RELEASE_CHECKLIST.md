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
6. App Review contact: App Store Connect requires a first name, last name, email and phone number as soon as a build is attached, and rejects the whole metadata upload without them. `deliver` sends them from `app_review_information` in the Fastfile. The name and email default to values already public in this repository's history; the phone number is deliberately not stored here because the repository is public, so every metadata run needs it in the environment:

```bash
cd ios
ASC_REVIEW_PHONE="+49..." bundle exec fastlane ios metadata
```

## Store assets

Screenshots live in `ios/fastlane/screenshots/{de-DE,en-US}` and cover both device classes App Review needs while `TARGETED_DEVICE_FAMILY` is `1,2`:

- iPhone 6.9 inch at 1320x2868, captured on a 6.9 inch simulator and composed into marketing frames with Higgsfield image to image.
- iPad 13 inch at 2064x2752, captured on an iPad Pro 13 inch simulator and composed by `ios/fastlane/compose_ipad_frames.py`.

Both sets show the untouched app capture inside the frame. Only the backdrop, the device shell and the headline are added, so the screenshots still represent what the app does. Regenerate the iPad set with `python3 ios/fastlane/compose_ipad_frames.py` after changing the captures.

The App Store icon comes from the uploaded build, not from a separate upload. It stays a placeholder in App Store Connect until the first build finishes processing.

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
