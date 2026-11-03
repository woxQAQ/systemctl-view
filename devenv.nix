{ pkgs, lib, ... }:

# mygo's Linux backend is pure Go: it dlopens GTK 3, WebKitGTK and Pango
# at run time (no cgo), so the libraries must sit on LD_LIBRARY_PATH for
# `go run` and the tests to find them.
let
  guiLibs = with pkgs; [
    glib
    gtk3
    cairo
    pango
    harfbuzz
    fontconfig
    gdk-pixbuf
    libsoup_3
    webkitgtk_4_1
    libayatana-appindicator
  ];
in
{
  # go.mod asks for 1.27; nixpkgs' default `go` is older.
  languages.go.enable = true;
  languages.go.package = pkgs.go_1_27;

  packages = with pkgs; [
    air # rebuild-and-rerun for development
  ];

  env.LD_LIBRARY_PATH = lib.makeLibraryPath guiLibs;

  # systemctl talks to the host's systemd over D-Bus; nothing to set up.

  scripts.dev.exec = "go run .";
  scripts.test.exec = "CGO_ENABLED=0 go test ./... && go tool mygo vet .";

  enterTest = ''
    CGO_ENABLED=0 go test ./...
    go tool mygo vet .
  '';
}
