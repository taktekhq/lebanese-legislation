// Command publish-release uploads a built release directory (manifest.json
// and corpus.dump, as build-release writes them) to the Hugging Face dataset
// in one commit: releases/<id>/manifest.json, releases/<id>/corpus.dump
// (Git LFS), the releases/latest.json pointer and the release block of the
// dataset card. It refuses to overwrite a published release id, then reads
// the release back anonymously to check it. Needs HF_TOKEN with write access.
//
//	publish-release -dir DIR [-repo owner/name] [-expect-latest ID]
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/taktekhq/lebanese-legislation/internal/hfhub"
	"github.com/taktekhq/lebanese-legislation/internal/release"
)

func main() {
	ctx, stop := release.Context()
	defer stop()
	dir := flag.String("dir", "", "the release directory (manifest.json and its dump)")
	repo := flag.String("repo", release.DefaultRepo, "the Hugging Face dataset")
	expect := flag.String("expect-latest", "", "refuse unless releases/latest.json names this release now (\"none\": no pointer yet)")
	card := flag.Bool("card", true, "update the release block of the dataset card (README.md) in the same commit")
	setup := flag.String("setup-url", "https://github.com/taktekhq/lebanese-legislation#run-the-services-locally", "the \"Run with Docker\" link in the card; empty for none")
	verify := flag.Bool("verify", true, "read the published release back anonymously and compare it")
	endpoint := flag.String("hub", release.Env("HF_ENDPOINT", hfhub.DefaultEndpoint), "the Hugging Face endpoint")
	flag.Parse()
	if *dir == "" {
		release.Exit(errors.New("-dir is required"))
	}
	token := os.Getenv("HF_TOKEN")
	if token == "" {
		release.Exit(errors.New("HF_TOKEN is required to publish"))
	}
	hub := hfhub.New(*endpoint, token)
	oid, r, err := release.Publish(ctx, hub, *dir, release.PublishOptions{Repo: *repo, ExpectLatest: *expect, Card: *card, SetupURL: *setup})
	release.Exit(err)
	if *verify {
		release.Exit(release.VerifyPublished(ctx, hfhub.New(*endpoint, ""), *repo, oid, r, filepath.Join(*dir, r.Dump.Path)))
	}
	out, _ := json.Marshal(map[string]string{"artifact_revision": oid, "release_id": r.ReleaseID})
	fmt.Println(string(out))
}
