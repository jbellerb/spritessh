.POSIX:

OUTDIR := bin

.PHONY: all
all: build

.PHONY: build
build:
	@echo "Building spritessh..."
	go build -o ${OUTDIR}/spritessh ./

.PHONY: clean
clean:
	rm -rf ${OUTDIR}
