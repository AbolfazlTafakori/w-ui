// This directory is the frontend's npm project, not Go. A go.mod of its own
// makes it a module apart, so "go build ./...", "go vet ./..." and
// "go test ./..." from the repository root never walk into its node_modules,
// where an npm package that ships Go code would otherwise be compiled into
// the panel's build and tests.
module github.com/abolfazl/w-ui/web

go 1.26.0
