#!/bin/sh
set -e

if [[ $# -eq 0 ]]; then
	echo "Instance name argument required"
	exit 1
fi

#GUS_ADDR=http://100.109.9.11:8655
GUS_ADDR=http://100.108.73.80:8655
# CGO_ENABLED=1 GOOS=linux GOARCH=arm64 ${GOPATH}/bin/gok --parent_dir ./gokrazy/ -i $1 overwrite --gaf ./$1.gaf
SBOM=$(unzip -p ./$1.gaf sbom.json | jq -r '.sbom_hash')
echo $SBOM
LINK=$(GOOS=linux GOARCH=arm64 ${GOPATH}/bin/gok --parent_dir ./gokrazy/ -i $1 push --gaf ./$1.gaf --server $GUS_ADDR --json | jq -r '.download_link')
echo $LINK
MACHINE_ID=$(cat ./gokrazy/${1}/config.json | jq -r '.PackageConfig."github.com/gokrazy/gokrazy/cmd/randomd".ExtraFileContents."/etc/machine-id"' | xargs)
echo $MACHINE_ID
curl -sL -d "{\"machine_id_pattern\": \"${MACHINE_ID}\", \"sbom_hash\": \"${SBOM}\" , \"registry_type\": \"localdisk\", \"download_link\": \"${LINK}\" }" -X POST $GUS_ADDR/api/v1/ingest
