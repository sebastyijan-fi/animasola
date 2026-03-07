package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	var privateOut string
	var publicOut string

	flag.StringVar(&privateOut, "private-out", "", "Path to write the base64-encoded Ed25519 private key")
	flag.StringVar(&publicOut, "public-out", "", "Path to write the base64-encoded Ed25519 public key")
	flag.Parse()

	if privateOut == "" || publicOut == "" {
		fmt.Fprintf(os.Stderr, "usage: %s -private-out path -public-out path\n", os.Args[0])
		os.Exit(1)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate keypair: %v\n", err)
		os.Exit(1)
	}

	if err := writeBase64File(privateOut, base64.StdEncoding.EncodeToString(priv)+"\n", 0600); err != nil {
		fmt.Fprintf(os.Stderr, "write private key: %v\n", err)
		os.Exit(1)
	}
	if err := writeBase64File(publicOut, base64.StdEncoding.EncodeToString(pub)+"\n", 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write public key: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Private key: %s\n", privateOut)
	fmt.Printf("Public key: %s\n", publicOut)
}

func writeBase64File(path, data string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), mode)
}
