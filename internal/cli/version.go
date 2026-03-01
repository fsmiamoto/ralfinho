package cli

// Version is the current build version, set at build time via ldflags.
// Falls back to "dev" for untagged / go run builds.
var Version = "dev"
