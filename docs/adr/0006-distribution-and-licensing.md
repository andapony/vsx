# Distribution and licensing

`vsx` is licensed **LGPL-3.0-or-later**, uniformly across its own code and the
vendored `rdac` codec (ADR-0004). Because the LGPL codec is statically linked
into the Go binary, a uniform LGPL license is the unambiguous, fully-compliant
choice — it avoids any question of relink rights or source availability when the
project is released. A permissive license on our own pipeline code was
considered and rejected: the distributed binary would still carry LGPL
obligations, so permissive terms would buy little practical freedom while
complicating the story for contributors and packagers.

## Distribution

- **Now (private repo):** build-from-source, plus personal prebuilt static
  binaries via `goreleaser`/Makefile for the platforms in use (darwin + linux,
  amd64 + arm64). Pure Go means one static binary per platform, no cgo.
- **On OSS release (repo made public once stable and useful):** `go install`
  from the public module path becomes viable, plus GitHub Releases carrying
  cross-platform static binaries.

## Note

LGPL obligations are dormant while the repo is private and undistributed; they
take effect on public release. The license is fixed now, before any contributors
exist, precisely because relicensing later would require every contributor's
agreement.
