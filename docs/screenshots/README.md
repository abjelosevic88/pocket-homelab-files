# README screenshots and the end-to-end run

`scripts/e2e.mjs` drives the built app in headless Chrome and writes the screenshots here. It also
checks the encrypted round trip: Chrome downloads the decrypted file into `/data/dl`, which must
equal the uploaded one.

```sh
make web build
d=$(mktemp -d); mkdir -p $d/server/Media/Movies $d/chrome/in $d/chrome/dl
head -c 300000 /dev/urandom > $d/chrome/in/random.bin; echo hello > $d/chrome/in/hello.txt; chmod -R 777 $d/chrome
HOST=127.0.0.1 PORT=18198 PUBLIC_PORT=18199 BASE_URL=http://127.0.0.1:18198 PUBLIC_URL=http://127.0.0.1:18199 \
  DATA_DIR=$d/data AUTH_MODE=none DEMO_DATA=true TZ=Europe/Sarajevo MOUNT_ROOTS=$d/server ./pocket-files &
curl -s -X POST 127.0.0.1:18198/api/v1/mounts -H 'Content-Type: application/json' \
  -d "{\"name\":\"Media\",\"path\":\"$d/server/Media\",\"everyone_perm\":\"view\"}"
docker run -d --rm --name pf-chrome --network host --shm-size=1g -v $d/chrome:/data zenika/alpine-chrome:124 \
  --no-sandbox --headless --disable-gpu --disable-dev-shm-usage --hide-scrollbars \
  --remote-debugging-port=19228 --remote-debugging-address=127.0.0.1 --remote-allow-origins='*' about:blank
node scripts/e2e.mjs && cmp $d/chrome/dl/random.bin $d/chrome/in/random.bin && echo round trip ok
docker stop pf-chrome
```
