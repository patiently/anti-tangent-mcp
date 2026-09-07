// Package notices exists so the repository's third-party attribution can be
// asserted by an ordinary `go test ./...` run rather than only in CI.
// Attribution is a licence obligation, so its accidental removal should break
// a developer's build, not merely a pipeline. The package has no runtime API;
// doc.go keeps it from being a test-only directory that `go vet` complains
// about.
package notices
