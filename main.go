package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mendableai/firecrawl-go"
	"github.com/sirupsen/logrus"
)

type Metadata struct {
	Input  MetadataInput  `json:"input"`
	Output MetadataOutput `json:"output"`
}

type MetadataInput struct {
	URL string `json:"url"`
}

type MetadataOutput struct {
	Status      string                 `json:"status"`
	Error       string                 `json:"error"`
	ScrapeJobId string                 `json:"scrape_job_id"`
	Pages       map[string]PageDetails `json:"pages"`
}

type PageDetails struct {
	Path       string `json:"path"`
	URL        string `json:"url"`
	LastUpdate string `json:"last_update"`
}

func main() {
	firecrawlUrl := os.Getenv("FIRECRAWL_URL")
	if firecrawlUrl == "" {
		firecrawlUrl = "http://localhost:3002"
	}
	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	if apiKey == "" {
		apiKey = "test"
	}
	app, err := firecrawl.NewFirecrawlApp(apiKey, firecrawlUrl)
	if err != nil {
		log.Fatalf("Failed to initialize FirecrawlApp: %v", err)
	}

	workingDir := os.Getenv("GPTSCRIPT_WORKSPACE_DIR")
	if workingDir == "" {
		workingDir, err = os.Getwd()
		if err != nil {
			logrus.Error(err)
			os.Exit(1)
		}
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

	if metadata.Output.ScrapeJobId == "" {
		crawlStatus, err := app.AsyncCrawlURL(metadata.Input.URL, &firecrawl.CrawlParams{
			Limit: &[]int{100}[0],
			ScrapeOptions: firecrawl.ScrapeParams{
				Formats: []string{"markdown"},
			},
		}, nil)
		if err != nil {
			log.Fatalf("Failed to send crawl request: %v", err)
		}
		metadata.Output.ScrapeJobId = crawlStatus.ID
		if err := writeMetadata(metadata, metadataPath); err != nil {
			log.Fatalf("Failed to write metadata: %v", err)
		}
	}

	fileWritten := 0
OuterLoop:
	for {
		crawlStatus, err := app.CheckCrawlStatus(metadata.Output.ScrapeJobId)
		if err != nil {
			log.Fatalf("Failed to check crawl status: %v", err)
		}
		if crawlStatus.Status == "completed" {
			for {
				for _, data := range crawlStatus.Data {
					source := ""
					if data.Metadata.SourceURL != nil {
						source = *data.Metadata.SourceURL
					}
					lastModified := time.Now().String()
					if data.Metadata.ModifiedTime != nil {
						lastModified = *data.Metadata.ModifiedTime
					}

					parsedURL, err := url.Parse(source)
					if err != nil {
						logrus.Errorf("Failed to parse URL %s: %v", source, err)
						continue
					}

					urlPath := ""
					if parsedURL.Path == "" {
						urlPath = "/" + parsedURL.Host
					} else {
						urlPath = parsedURL.Path
					}

					existingPage, ok := metadata.Output.Pages[urlPath]
					if ok && existingPage.LastUpdate == lastModified && lastModified != "" {
						continue
					}

					markdownPath := filepath.Join(workingDir, parsedURL.Host, urlPath) + ".md"

					if err := os.MkdirAll(filepath.Dir(markdownPath), 0755); err != nil {
						logrus.Errorf("Failed to create directory for %s: %v", markdownPath, err)
						continue
					}

					if err := os.WriteFile(markdownPath, []byte(data.Markdown), 0644); err != nil {
						logrus.Errorf("Failed to write markdown file %s: %v", markdownPath, err)
						continue
					}

					metadata.Output.Pages[urlPath] = PageDetails{
						LastUpdate: lastModified,
						Path:       markdownPath,
						URL:        source,
					}
					fileWritten++
					logrus.Infof("wrote %d webpages to disk", fileWritten)
					metadata.Output.Status = fmt.Sprintf("wrote %d webpages to disk", fileWritten)

					if err := writeMetadata(metadata, metadataPath); err != nil {
						logrus.Fatalf("Failed to write metadata: %v", err)
					}
				}
				if crawlStatus.Next == nil || *crawlStatus.Next == "" {
					break OuterLoop
				} else {
					if !strings.HasPrefix(*crawlStatus.Next, "http://") {
						*crawlStatus.Next = "http://" + strings.TrimPrefix(*crawlStatus.Next, "https://")
					}

					req, err := http.NewRequest(http.MethodGet, *crawlStatus.Next, nil)
					if err != nil {
						log.Fatalf("Failed to create request: %v", err)
					}
					req.Header.Add("Content-Type", "application/json")
					client := &http.Client{}
					var respBody []byte
					for i := 0; i < 3; i++ {
						resp, err := client.Do(req)
						if err != nil {
							time.Sleep(500 * time.Millisecond)
							continue
						}
						defer resp.Body.Close()
						respBody, err = io.ReadAll(resp.Body)
						if err != nil {
							log.Fatalf("Failed to read response body: %v", err)
						}
						break
					}
					if err != nil {
						log.Fatalf("Failed to get next crawl status: %v", err)
					}
					newCrawlStatus := firecrawl.CrawlStatusResponse{}
					err = json.Unmarshal(respBody, &newCrawlStatus)
					if err != nil {
						log.Fatalf("Failed to unmarshal next crawl status: %v", err)
					}
					crawlStatus = &newCrawlStatus
				}
			}
		} else {
			metadata.Output.Status = fmt.Sprintf("crawling status: %s, completed %d, total %d", crawlStatus.Status, crawlStatus.Completed, crawlStatus.Total)
			if err := writeMetadata(metadata, metadataPath); err != nil {
				log.Fatalf("Failed to write metadata: %v", err)
			}
			continue
		}
	}

	metadata.Output.Status = "done"
	if err := writeMetadata(metadata, metadataPath); err != nil {
		log.Fatalf("Failed to write metadata: %v", err)
	}
}

func writeMetadata(metadata Metadata, path string) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
