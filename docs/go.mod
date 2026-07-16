// This stub turns docs/ into a separate (empty) module so that the parent
// module's go-get zip excludes everything in this directory. Reference
// material like Kamstrup_M-Bus_and_wM-Bus_Protocol.pdf stays in the repo
// for browsing on GitHub but is not downloaded by `go get github.com/yottabytesolutions/gombus`.
//
// See https://go.dev/ref/mod#zip-files: subdirectories that contain a
// go.mod file are excluded from the parent module zip.
module github.com/yottabytesolutions/gombus/docs

go 1.26
