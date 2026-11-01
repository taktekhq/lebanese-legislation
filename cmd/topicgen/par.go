package main

import "sync"

// forEach runs f over the items, -j at a time.
func forEach(items []string, f func(string)) {
	sem := make(chan struct{}, max(*parallel, 1))
	var wg sync.WaitGroup
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			f(it)
		}()
	}
	wg.Wait()
}

func cut2(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
