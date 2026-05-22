# databrowser

Local server and dashboard for Marlin data.

## Address

```bash
# over local WiFi
http://192.168.1.1:7144
```

## API and Entrypoints

### /data/

```
/data/
/data/2026-05-05/
/data/2026-05-05/gps/
/data/2026-05-05/gps/gps-2026-05-05T16_08_31Z.json
```

Serves directory entries as a json array.

### /rawdata/

```
/rawdata/
/rawdata/2026-05-05/
/rawdata/2026-05-05/gps/
/rawdata/2026-05-05/gps/gps-2026-05-05T16_08_31Z.json
```

Serves directory entries as HTML.

### /logs/

Serves logs errors sent by the collector.

#### POST to /logs/?v=value

To send value as a new log entry.

### /

Serves the dashboard code.

### ?report

```
?report=2024-07-27
```

gives a report about missing data on that specific day

```
?report=anyotherstring
```

gives a report about missing data on the last known day at sea

## Fetch with wget

### Using wget on Unix

```bash
wget -r -np -R "index.html*" http://192.168.1.1:7144/rawdata/2024-07-27
```

### Installing wget on Windows

Follow the instruction on <https://gnuwin32.sourceforge.net/packages/wget.htm> , or simply go to <https://sourceforge.net/projects/gnuwin32/files/wget/> , pick a version, then download the `wget-version-setup.exe` one.


### Using wget on Windows

```
"C:\Program Files\GnuWin\bin\wget" -r -np -R "index.html*" http://192.168.1.1:7144/rawdata/2024-07-27
```
