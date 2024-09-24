package main

import (
	"encoding/json"
	"os"
	"path"

	"github.com/sirupsen/logrus"
)

type Metadata struct {
	Input  MetadataInput  `json:"input"`
	Output MetadataOutput `json:"output"`
}

type MetadataInput struct {
	URLs []string `json:"urls"`
}

type MetadataOutput struct {
	Status       string                 `json:"status"`
	Error        string                 `json:"error"`
	ScrapeJobIds map[string]string      `json:"scrape_job_ids"`
	Pages        map[string]PageDetails `json:"pages"`
	Folders      map[string]struct{}    `json:"folders"`
}

type PageDetails struct {
	Path       string `json:"path"`
	URL        string `json:"url"`
	LastUpdate string `json:"last_update"`
}

func main() {
	var err error
	workingDir := os.Getenv("GPTSCRIPT_WORKSPACE_DIR")
	if workingDir == "" {
		workingDir, err = os.Getwd()
		if err != nil {
			logrus.Error(err)
			os.Exit(1)
		}
	}

	mode := os.Getenv("MODE")
	if mode == "" {
		mode = "colly"
	}

	metadata := Metadata{}
	metadataPath := path.Join(workingDir, ".metadata.json")
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		logrus.Fatal("metadata.json not found")
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		logrus.Fatal(err)
	}

	err = json.Unmarshal(data, &metadata)
	if err != nil {
		logrus.Fatal(err)
	}

	if metadata.Output.Pages == nil {
		metadata.Output.Pages = make(map[string]PageDetails)
	}

	if mode == "colly" {
		NewColly().Crawl(&metadata, metadataPath, workingDir)
	} else if mode == "firecrawl" {
		NewFirecrawl().Crawl(&metadata, metadataPath, workingDir)
	}
}

func writeMetadata(metadata *Metadata, path string) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
