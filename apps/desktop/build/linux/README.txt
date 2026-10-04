Calendium for Linux
===================

This tarball contains the Calendium binary, a .desktop entry, and a 512 px icon.
Install for the current user:

  install -Dm755 Calendium ~/.local/bin/Calendium
  install -Dm644 calendium.png ~/.local/share/icons/hicolor/512x512/apps/calendium.png
  install -Dm644 calendium.desktop ~/.local/share/applications/calendium.desktop
  xdg-mime default calendium.desktop x-scheme-handler/calendium
  update-desktop-database ~/.local/share/applications

The xdg-mime line registers the calendium:// scheme so sign-in and
mailbox-connect links from your browser open in the app (a second launch
forwards the link to the running instance). ~/.local/bin must be on PATH for
"Exec=Calendium %u" to resolve; otherwise edit Exec= to the full path.

The desktop app checks GitHub once a day for a newer release and shows a
banner; set CALENDIUM_UPDATE_URL=off to disable.
