# State privacy information

Last updated: August 11, 2026

State is a client for a self-hosted reminder and agent-context service.

## Data processed by your server

Your chosen State server processes reminder titles, descriptions, schedules, recurrence rules, comments, actor identities, device labels, source excerpts, revision data, conflict snapshots and audit history. This processing enables synchronization, search and MCP access.

The operator of that server controls its storage, retention and backups. The State project does not receive that content unless the operator also runs infrastructure controlled by the project maintainer.

## Shared push relay

The optional shared relay processes an opaque route, encrypted notification packages, delivery metadata, rate-limit counters and App Attest material. Reminder plaintext and APNs device tokens are not written to relay logs. APNs tokens are encrypted at rest. Notification content is encrypted for the destination device before it reaches the relay.

Apple Push Notification service necessarily receives the APNs destination and encrypted notification envelope required for delivery.

## On-device data

The app stores synchronized content, pending offline mutations, conflicts and cryptographic material on the device. Credentials and private keys use the iOS Keychain. State schedules upcoming notifications locally so they remain available during a server outage.

## Analytics and advertising

Version 1 contains no advertising SDK, cross-app tracking or third-party product analytics.

## Retention and deletion

The owner can archive and restore reminders. Server operators control database and backup retention. Removing the app deletes its local application container. Keychain behavior follows iOS rules. Harness access can be revoked from the app or with `statectl`.

## Contact

Privacy questions can be submitted through the repository issue tracker without including personal reminder content or credentials.
