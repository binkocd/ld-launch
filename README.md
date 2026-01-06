# LD-Launch

A tool to help manage multiple docker hosts from a single instance of lazydocker.

## Prerequisites
- Ubuntu 22.04 - Tested
- [GoLang](https://go.dev/doc/install)


## Build
```bash
git clone https://github.com/binkocd/ld-launch.git
cd ld-launch
```
```bash
go mod init ld-launch
```
```bash
go get github.com/charmbracelet/bubbletea \
       github.com/charmbracelet/bubbles/list \
       github.com/charmbracelet/lipgloss \
       github.com/charmbracelet/bubbles/textinput \
       gopkg.in/yaml.v3
```
```bash
go build -o ld-launch -buildvcs=false
```

## Usage

```bash
./ld-launch
```
Otherwise...
```bash
mv ld-launch /usr/local/bin/
```

## Contributing

TBD

## License

TBD