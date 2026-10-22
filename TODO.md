# todo

**info** tasks with - are todo, tasks marked with + are done!

+ this project needs refactor.
  + the user flow is to open the app & adjust settings (what screen/window/rectangle to record)
  + it shall be a mac app (swift) that is in menu bar and as a little app window.
  + when open, the app hosts itself as a mcp server target to be configured and used in opencode. add instructions in app.
  + the app itself shall request required access
  + the app then provides control to any agent via mcp
  + mcp server shall be written in go. go and swift app need to communicated
+ the menubar window shows 4 rows perfectly & the other hide for a short moment every ~2seconds. — obsolete: pre-Swift-app note; the 2s timer now stops on window close (d641990) and mode rows no longer snap back (0070436). Verified 2026-10-07: 30 UI samples over 15s, zero flicker/stalls.
+ window list is in alphabetical order — `window.Sort` (app, then title, case-insensitive, stable) applied in the `list_windows` handler, so the tool and the app picker both get it. Verified live 2026-10-09: 36 windows, SORTED OK.
+ on app start, screen reader shall start with timer running. so an opt-out mechanism. also when starting the server, make timer it opt-out — 4h auto-stop deadline is scheduled at app start and on every explicit server start; cleared when the server stops/crashes. Menu items: "Server: start"/"Server: stop" and "Timer: start"/"Timer: clear (server stop in H:MM)" where H:MM is the 4h timer's current value (time remaining); the timer item is hidden while the server is down. Verified by user 2026-10-09.
+ while server is running, menu bar icon has an indicator — dot badge (MenuBadge, pure CoreGraphics pixel loop, 3 unit tests) at the icon's top-right, shown while the server is up, swapped in place in updateMenu() on state flip (template ink, no-teardown menu rule kept). Verified by user 2026-10-09.
