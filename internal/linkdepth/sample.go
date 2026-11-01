// SPDX-License-Identifier: MIT

package linkdepth

import (
	"encoding/xml"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
)

type sitemapDoc struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

func getXML(c *http.Client, ua, u string) (*sitemapDoc, error) {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var d sitemapDoc
	return &d, xml.Unmarshal(b, &d)
}

// Sample picks n page addresses from a site's sitemaps, spread evenly over the
// sitemap files the index lists: it reads at most maxShards of them (a random
// choice with the seed) and draws the same number of addresses from each, so
// a 45,000-address file does not outweigh a 900-address one. Sitemap files
// are fetched one after another, once each. Only the path is returned.
func Sample(c *http.Client, ua, base string, n, maxShards int, seed uint64) ([]string, error) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	idx, err := getXML(c, ua, strings.TrimRight(base, "/")+"/sitemap.xml")
	if err != nil {
		return nil, err
	}
	var shards []string
	for _, s := range idx.Sitemaps {
		shards = append(shards, s.Loc)
	}
	if len(shards) == 0 {
		return nil, fmt.Errorf("no sitemap files in the index")
	}
	sort.Strings(shards)
	rng.Shuffle(len(shards), func(i, j int) { shards[i], shards[j] = shards[j], shards[i] })
	if len(shards) > maxShards {
		shards = shards[:maxShards]
	}
	per := max(n/len(shards), 1)
	var out []string
	for _, loc := range shards {
		d, err := getXML(c, ua, loc)
		if err != nil {
			return nil, err
		}
		perm := rng.Perm(len(d.URLs))
		for _, i := range perm[:min(per, len(perm))] {
			u, err := url.Parse(strings.TrimSpace(d.URLs[i].Loc))
			if err == nil {
				out = append(out, u.EscapedPath())
			}
		}
	}
	return out, nil
}

// ReadList reads addresses, one path per line, skipping blank lines and lines
// that start with #.
func ReadList(file string) ([]string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out, nil
}
