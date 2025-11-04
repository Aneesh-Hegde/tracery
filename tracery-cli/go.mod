module github.com/Aneesh-Hegde/tracery/tracery-cli

go 1.24.9

replace github.com/Aneesh-Hegde/tracery/control-plane => ../control-plane

require (
	github.com/Aneesh-Hegde/tracery/control-plane v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.76.0
)

require (
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250825161204-c5933d9347a5 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)
