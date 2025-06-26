# databrowser

Simple HTTP server for Marlin data.

## Address

```bash
http://core.local:8363
```

## Fetch with wget

### Using wget on Unix

```bash
wget -r -np -R "index.html*" http://core.local:8363/2024-07-27
```

### Installing wget on Windows

Follow the instruction on <https://gnuwin32.sourceforge.net/packages/wget.htm> , or simply go to <https://sourceforge.net/projects/gnuwin32/files/wget/> , pick a version, then download the `wget-version-setup.exe` one.


### Using wget on Windows

```
"C:\Program Files\GnuWin\bin\wget" -r -np -R "index.html*" http://core.local:8363/2024-07-27
```

### Entrypoints

#### ?report

```
?report=2024-07-27
```

gives a report about missing data on that specific day

```
?report=anyotherstring
```

gives a report about missing data on the last known day at sea
