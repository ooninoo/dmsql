module github.com/ooninoo/dmsql

go 1.26.3

require (
	dm v0.0.0
	golang.org/x/term v0.45.0
)

require (
	github.com/golang/snappy v0.0.1 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace dm => ./pkg/dmsdk
