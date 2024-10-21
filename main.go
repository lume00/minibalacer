package main

import (
	gobalancer "minibalancer/internal"
)

func main() {
	err := gobalancer.FromFile()
	if err != nil {
		panic(err)
	}
}
