package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"ZiniGo/zinio"
)

func main() {
	usernamePtr := flag.String("u", "", "Zinio Username")
	passwordPtr := flag.String("p", "", "Zinio Password")
	deviceFingerprintPtr := flag.String("fingerprint", "abcd123", "Device fingerprint")
	newsstandIDPtr := flag.Int("ns", 101, "Newsstand ID")
	onlyIssuePtr := flag.Int("issue", 0, "Only process this issue ID (0 = all)")

	flag.Parse()

	if err := zinio.InitPDF(); err != nil {
		log.Fatal(err)
	}
	defer zinio.ClosePDF()

	mydir, err := os.Getwd()
	if err != nil {
		fmt.Println(err)
	}

	if data, readErr := os.ReadFile(filepath.Join(mydir, "config.json")); readErr == nil {
		fmt.Println("Config file loaded")
		if u := gjson.GetBytes(data, "username"); u.Exists() {
			*usernamePtr = u.String()
			fmt.Println("Username taken from config file")
		}
		if p := gjson.GetBytes(data, "password"); p.Exists() {
			*passwordPtr = p.String()
			fmt.Println("Password taken from config file")
		}
		if fp := gjson.GetBytes(data, "fingerprint"); fp.Exists() {
			*deviceFingerprintPtr = fp.String()
			fmt.Println("Fingerprint taken from config file")
		} else {
			fmt.Println("No fingerprint in config, generating one")
			newJson, _ := sjson.Set(string(data), "fingerprint", randSeq(15))
			if writeErr := os.WriteFile("config.json", []byte(newJson), 0644); writeErr != nil {
				log.Fatalf("unable to write file: %v", writeErr)
			}
			*deviceFingerprintPtr = gjson.Get(newJson, "fingerprint").String()
		}
	}

	if *usernamePtr == "" || *passwordPtr == "" {
		log.Fatal("Username and password are required. Use -u and -p flags or config.json")
	}

	client, err := zinio.NewClient(*usernamePtr, *passwordPtr, *deviceFingerprintPtr, *newsstandIDPtr)
	if err != nil {
		log.Fatal(err)
	}
	client.Logf = func(format string, args ...interface{}) {
		fmt.Printf(format+"\n", args...)
	}

	fmt.Println("Logging in...")
	email, err := client.Login()
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}
	fmt.Println("Logged in as:", email)

	issueDirectory := filepath.Join(mydir, "issue")

	issues, err := client.Issues()
	if err != nil {
		log.Fatalf("Failed to fetch library: %v", err)
	}
	fmt.Printf("Found %d issues in library\n", len(issues))

	for _, issue := range issues {
		if *onlyIssuePtr != 0 && issue.ID != *onlyIssuePtr {
			continue
		}
		completeName := filepath.Join(issueDirectory, issue.FileName())
		if _, statErr := os.Stat(completeName); statErr == nil {
			fmt.Println("Already exists:", completeName)
			continue
		}

		fmt.Printf("Downloading: %s - %s\n", issue.PublicationName, issue.Name)
		lastDone := -1
		outPath, dlErr := client.DownloadIssue(context.Background(), issue, issueDirectory, func(p zinio.Progress) {
			if p.PagesDone != lastDone && p.PagesDone%25 == 0 {
				fmt.Printf("  %d/%d pages\n", p.PagesDone, p.PagesTotal)
				lastDone = p.PagesDone
			}
		})
		if dlErr != nil {
			fmt.Printf("Skipping %s: %v\n", completeName, dlErr)
			continue
		}
		fmt.Println("Saved:", outPath)
	}

	fmt.Println("Done.")
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")

func randSeq(n int) string {
	rand.Seed(time.Now().UnixNano())
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
