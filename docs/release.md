# Release Checklist

Use this checklist for tagged GoKG releases.

## Preflight

1. Confirm the working tree contains only intended changes.
2. Run tests:

   ```bash
   go test ./...
   ```

3. Run the self-analysis baseline:

   ```bash
   gokg analyze --db /tmp/gokg-public-baseline --rebuild --tests
   ```

4. Validate GoReleaser configuration:

   ```bash
   goreleaser check
   ```

5. Optionally run a local snapshot release:

   ```bash
   goreleaser release --snapshot --clean --skip=publish
   ```

## Tag

Use semantic version tags. Alpha releases should include an alpha suffix.

```bash
git tag v0.1.0-alpha.1
git push origin v0.1.0-alpha.1
```

## Verify

After the GitHub Actions release workflow finishes:

1. Confirm the GitHub Release exists.
2. Confirm release assets exist for:
   - `darwin/amd64`
   - `darwin/arm64`
   - `linux/amd64`
   - `linux/arm64`
   - `windows/amd64`
3. Confirm the checksum file is attached.
4. Download one archive, run `gokg version`, and confirm the version matches the tag.
5. Update `CHANGELOG.md` for the next development cycle.
