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

## App Store Connect decisions

Complete these settings in App Store Connect before App Review. They are not inferred or submitted by Fastlane.

1. App Privacy: answer the questionnaire using `PRIVACY.md` and the actual relay deployment. A self-hosted server controlled only by the customer and the optional shared relay have different disclosure implications. Confirm the final answers with the person responsible for the service and privacy information.
2. Age Rating: complete the Apple questionnaire from the actual feature set. State has reminders, notes and agent context, but the rating still needs an account-holder attestation.
3. Pricing and Availability: set the app to Free and select the intended storefronts. Worldwide availability is a product and legal decision, not an automation default.
4. Export Compliance: confirm the current plist declaration and review lane fields. State uses platform cryptography for transport and encrypted notifications, so do not submit the declaration without checking Apple's current questionnaire.
5. App Review contact: enter the support contact details and keep the included demo-mode review instructions.

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
5. Add Fabian as an internal tester in App Store Connect and install that exact TestFlight build on a physical iPhone.
6. Pair the iPhone with the production State server, create a reminder through MCP, edit it offline in the app, sync, and verify the full activity history.
7. Verify local notification scheduling while the VPS is unavailable. Verify APNs only after production APNs credentials and the permanent relay domain are enabled.

## Before public App Review

- Replace the temporary `sslip.io` endpoints with the permanent production domains.
- Enable production APNs credentials and turn off relay dry-run mode.
- Complete a physical-device accessibility pass for German and English, Dark Mode, Dynamic Type and VoiceOver.
- Enable HSTS only after successful owner pairing and the final TLS smoke test.
- Run and document an encrypted backup restore test for the deployed server.
