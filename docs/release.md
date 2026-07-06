# Release Checklist

Use this checklist for tagged GoKG releases.

## Preflight

1. Confirm the working tree contains only intended changes.
2. Confirm the package manager repositories exist:
   - `hungpdn/homebrew-tap`
   - `hungpdn/scoop-bucket`
3. Confirm the release workflow has access to these repository secrets:
   - `PUBLISH_GITHUB_TOKEN`

   A fine-grained GitHub token needs `Contents: Read and write` access to the target tap or bucket repository. The same token can be reused for both secrets if it has access to both repositories.

4. Run the full local quality gate:

   ```bash
   RUN_RACE=1 RUN_LINT=1 RUN_VULN=1 SMOKE_TESTS=1 ./scripts/check.sh
   ```

5. Run the self-analysis baseline:

   ```bash
   gokg analyze --db /tmp/gokg-public-baseline --rebuild --tests
   ```

6. Validate GoReleaser configuration:

   ```bash
   goreleaser check
   ```

7. Optionally run a local snapshot release:

   ```bash
   goreleaser release --snapshot --clean --skip=publish
   ```

## Tag

Use a semantic version tag from `main`. Keep the prerelease suffix for alpha or release-candidate builds; remove it only for the final release.

```bash
TAG=v0.1.0-rc.1
git tag "$TAG"
git push origin "$TAG"
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
4. Confirm the Homebrew tap was updated with `Casks/gokg.rb`.
5. Confirm the Scoop bucket was updated with `gokg.json`.
6. Download one archive, run `gokg version`, and confirm the version matches the tag.
7. Test the Go install path:

   ```bash
   TAG=v0.1.0-rc.1
   go install github.com/hungpdn/gokg/cmd/gokg@"$TAG"
   gokg version
   ```

8. Test package manager installs:

   ```bash
   brew tap hungpdn/tap
   brew install --cask gokg
   gokg version
   ```

   ```powershell
   scoop bucket add hungpdn https://github.com/hungpdn/scoop-bucket.git
   scoop install hungpdn/gokg
   gokg version
   ```

9. Update `CHANGELOG.md` for the next development cycle.
