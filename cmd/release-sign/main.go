package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	var keyFile string
	var inputFile string
	var outputFile string

	flag.StringVar(&keyFile, "key-file", "", "Path to a base64-encoded Ed25519 private key file")
	flag.StringVar(&inputFile, "input", "", "Path to the file to sign")
	flag.StringVar(&outputFile, "output", "", "Path to write the base64-encoded signature")
	flag.Parse()

	if keyFile == "" || inputFile == "" || outputFile == "" {
		fmt.Fprintf(os.Stderr, "usage: %s -key-file path -input file -output signature\n", os.Args[0])
		os.Exit(1)
	}

	priv, err := loadPrivateKey(keyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load signing key: %v\n", err)
		os.Exit(1)
	}

	data, err := os.ReadFile(inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read input: %v\n", err)
		os.Exit(1)
	}

	sig := ed25519.Sign(priv, data)
	if err := os.WriteFile(outputFile, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "write signature: %v\n", err)
		os.Exit(1)
	}
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keyBytes, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("decode private key: %w", err)
		}
		switch len(keyBytes) {
		case ed25519.SeedSize:
			return ed25519.NewKeyFromSeed(keyBytes), nil
		case ed25519.PrivateKeySize:
			return ed25519.PrivateKey(keyBytes), nil
		default:
			return nil, fmt.Errorf("unexpected private key length %d", len(keyBytes))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("private key not configured")
}
