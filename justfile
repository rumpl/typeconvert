default:
    @just --list 

# Build
build:
    go build -o typeconvert .

e2e:
    go test ./e2e
