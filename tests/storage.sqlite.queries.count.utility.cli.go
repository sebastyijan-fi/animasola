package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: count_utility <db_path>")
		os.Exit(1)
	}
	dbPath := os.Args[1]

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Printf("0\n")
		os.Exit(0)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if err != nil {
		fmt.Printf("0\n")
		os.Exit(0)
	}

	fmt.Printf("%d\n", count)
}
