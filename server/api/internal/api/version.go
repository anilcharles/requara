package api

// Version is the server release, set at build time:
//
//	go build -ldflags "-X github.com/anilcharles/requara/server/api/internal/api.Version=1.4.0"
var Version = "dev"

// APIVersion goes up when the HTTP API changes in a way older desktop apps
// can't handle. The desktop app compares it with the range it supports.
const APIVersion = 1
