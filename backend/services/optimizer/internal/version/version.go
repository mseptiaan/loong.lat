// Package version identifies the optimizer build. The solver binary that runs in
// the sandbox and the API that ships jobs to it must agree on it, so a quote
// can always be traced to the exact engine that produced it.
package version

// Engine is set at link time (-ldflags "-X github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/version.Engine=<git sha>").
var Engine = "dev"
