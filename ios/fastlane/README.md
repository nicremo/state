fastlane documentation
----

# Installation

Make sure you have the latest version of the Xcode command line tools installed:

```sh
xcode-select --install
```

For _fastlane_ installation instructions, see [Installing _fastlane_](https://docs.fastlane.tools/#installing-fastlane)

# Available Actions

## iOS

### ios diagnose

```sh
[bundle exec] fastlane ios diagnose
```

Verify the local release toolchain and run all tests

### ios create_app

```sh
[bundle exec] fastlane ios create_app
```

Create the App Store Connect application if it does not exist

### ios asc_status

```sh
[bundle exec] fastlane ios asc_status
```

Verify App Store Connect access and the State app record

### ios builds

```sh
[bundle exec] fastlane ios builds
```

Print the latest TestFlight and App Store build numbers

### ios privacy

```sh
[bundle exec] fastlane ios privacy
```

Validate the local App Store privacy declaration

### ios paths

```sh
[bundle exec] fastlane ios paths
```

Print the resolved release asset paths

### ios test

```sh
[bundle exec] fastlane ios test
```

Run unit and UI tests

### ios build

```sh
[bundle exec] fastlane ios build
```

Create a signed App Store archive

### ios beta

```sh
[bundle exec] fastlane ios beta
```

Upload an internal TestFlight build

### ios screenshots

```sh
[bundle exec] fastlane ios screenshots
```

Capture deterministic German and English 6.9-inch screenshots

### ios metadata

```sh
[bundle exec] fastlane ios metadata
```

Upload localized metadata, age rating and screenshots

### ios review

```sh
[bundle exec] fastlane ios review
```

Submit the uploaded build for App Review

### ios release

```sh
[bundle exec] fastlane ios release
```

Build, upload, update metadata and submit

### ios live_check

```sh
[bundle exec] fastlane ios live_check
```

Report the latest live App Store version

### ios review_status

```sh
[bundle exec] fastlane ios review_status
```

Print the App Store version and review submission state

### ios pricing

```sh
[bundle exec] fastlane ios pricing
```

Make State a free app with Germany as the base territory

----

This README.md is auto-generated and will be re-generated every time [_fastlane_](https://fastlane.tools) is run.

More information about _fastlane_ can be found on [fastlane.tools](https://fastlane.tools).

The documentation of _fastlane_ can be found on [docs.fastlane.tools](https://docs.fastlane.tools).
