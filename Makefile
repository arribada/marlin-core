# entrypoints to build everything in docker

.PHONY: validinstance
validinstance: ## check that INSTANCE is defined
validinstance:
	@ls gokrazy/${INSTANCE}/config.json

# TODO: make it a dep of more targets?
.PHONY: fsck
fsck: ## generate arm64 fsck
fsck: dosfstools/fsck.fat

dosfstools/fsck.fat:
	@cd dosfstools && make

.PHONY: gaf
gaf: ## generate gaf
gaf: fsck validinstance
	@docker build --progress=plain -f Dockerfile --build-arg gitcreds=${GITCREDS} --build-arg instance=${INSTANCE} --build-arg GOPROXY=${GOPROXY} --target gaf --output=. .

.PHONY: update
update: ## push update to GUS server
update: validinstance
	@make gaf
	@./bin/ingest.sh ${INSTANCE}

.PHONY: forceupdate
forceupdate: ## push update directly to device, usually not what you want.
forceupdate: fsck validinstance
	@docker build --progress=plain -f Dockerfile --build-arg gitcreds=${GITCREDS} --build-arg instance=${INSTANCE} --build-arg GOPROXY=${GOPROXY} --target forceupdate .

.PHONY: builddir
builddir: ## extract builddir
builddir: validinstance
	@docker build --progress=plain -f Dockerfile --build-arg gitcreds=${GITCREDS} --build-arg instance=${INSTANCE} --build-arg GOPROXY=${GOPROXY} --target builddir --output=./builddir .

.PHONY: artifact
artifact: ## extract an artifact
artifact: validinstance
	@docker build --progress=plain -f Dockerfile --build-arg gitcreds=${GITCREDS} --build-arg instance=${INSTANCE} --build-arg GOPROXY=${GOPROXY} --target artifact --output=./artifact .

#.PHONY: breakglass
#breakglass: ## breaking glass
#breakglass: ./bin/${TRANSCRIBE}
#	@cd ./bin && tar cf ../breakglass.tar ${TRANSCRIBE}
#	@breakglass -debug_tarball_pattern=breakglass.tar hello

.PHONY: wip
wip: ## testing stuff
wip: validinstance
	@docker build --progress=plain -f Dockerfile --build-arg gitcreds=${GITCREDS} --build-arg instance=${INSTANCE} --build-arg GOPROXY=${GOPROXY} --target wip --output=. .

.PHONY: athens
athens: ## start Athens (GOPROXY)
athens:
	@./bin/athens_launcher.sh

### the recipes below are the ones for when we are already within docker, so they are called from within the Dockerfile ###

.PHONY: gaf2
gaf2: ## create gaf for ingestion
gaf2: validinstance
	@CGO_ENABLED=1 GOOS=linux GOARCH=arm64 ${GOPATH}/bin/gok --parent_dir ./gokrazy/ -i ${INSTANCE} overwrite --gaf /tmp/${INSTANCE}.gaf

.PHONY: update2
update2: ## push update to GUS server
update2: validinstance
	@make gaf2
	@./bin/ingest.sh ${INSTANCE}

.PHONY: forceupdate2
forceupdate2: ## push update directly to device, usually not what you want.
forceupdate2: validinstance
	@CGO_ENABLED=1 GOOS=linux GOARCH=arm64 ${GOPATH}/bin/gok update --parent_dir=./gokrazy -i ${INSTANCE}

default: update
