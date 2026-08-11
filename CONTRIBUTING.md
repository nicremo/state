# Contributing

Contributions are welcome under the Apache-2.0 license.

## Development checks

```bash
gofmt -w cmd internal
go vet ./...
go test -race ./...
docker compose -f deploy/compose.yaml config --quiet
```

For iOS changes:

```bash
cd ios
xcodegen generate
xcodebuild -project State.xcodeproj -scheme State \
  -destination 'platform=iOS Simulator,name=iPhone 16 Pro' \
  CODE_SIGNING_ALLOWED=NO test
```

Commit messages use English conventional prefixes such as `feat:`, `fix:`, `docs:` and `refactor:`. Keep changes focused. Do not include credentials, generated signing assets or personal data.
