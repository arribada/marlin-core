#!/bin/sh

git config --global url."https://user:${1}@github.com".insteadOf "https://github.com"
