# Price Deal Hunter

## Get Started

```bash
go run main.go
```

```bash
go build
```

## Testing

Run the unit tests with:
```bash
go test -v main_test.go main.go
```

## Docker

Build the image:
```bash
docker build -t hunter-base .
```

Run the container:
```bash
docker run --rm -p 9090:9090 hunter-base
```

### Custom Domain (Optional)

To access the app via `http://hunterbase:9090` instead of `localhost`:

1. Open Notepad as Administrator.
2. Edit `C:\Windows\System32\drivers\etc\hosts`.
3. Add this line: `127.0.0.1 hunterbase`
