package main

import (
	"encoding/json"
	"log"
	url2 "net/url"
	"os"
	"path"
	"strings"
	"time"

	"io"
	"net/http"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/gocolly/colly"
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
	var err error
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

	collector := colly.NewCollector()
	converter := md.NewConverter("", true, nil)

	visited := make(map[string]bool)

	for _, url := range metadata.Input.URLs {
		collector.OnHTML("body", func(e *colly.HTMLElement) {
			if visited[e.Request.URL.String()] {
				return
			}
			logrus.Infof("scraping %s", e.Request.URL.String())
			visited[e.Request.URL.String()] = true
			markdown := converter.Convert(e.DOM)
			hostname := e.Request.URL.Hostname()
			urlPath := e.Request.URL.Path

			var filePath string
			if urlPath == "/" {
				filePath = path.Join(workingDir, hostname, "index.md")
			} else {
				trimmedPath := strings.Trim(urlPath, "/")
				segments := strings.Split(trimmedPath, "/")
				fileName := segments[len(segments)-1] + ".md"
				filePath = path.Join(path.Join(workingDir, hostname, strings.Join(segments[:len(segments)-1], "/")), fileName)
			}
			dirPath := path.Dir(filePath)
			err := os.MkdirAll(dirPath, os.ModePerm)
			if err != nil {
				logrus.Errorf("Failed to create directories for %s: %v", dirPath, err)
				return
			}

			err = os.WriteFile(filePath, []byte(markdown), 0644)
			if err != nil {
				logrus.Errorf("Failed to write markdown to %s: %v", filePath, err)
				return
			}
			visited[e.Request.URL.String()] = true

			metadata.Output.Pages[e.Request.URL.Path] = PageDetails{
				Path:       filePath,
				URL:        e.Request.URL.String(),
				LastUpdate: time.Now().String(),
			}
		})

		collector.OnHTML("a[href]", func(e *colly.HTMLElement) {
			link := e.Attr("href")
			baseURL, err := url2.Parse(url)
			if err != nil {
				logrus.Errorf("Invalid base URL: %v", err)
				return
			}
			linkURL, err := url2.Parse(link)
			if err != nil {
				logrus.Errorf("Invalid link URL %s: %v", link, err)
				return
			}
			if strings.ToLower(path.Ext(linkURL.Path)) == ".pdf" {
				logrus.Infof("downloading PDF %s", linkURL.String())
				filePath := path.Join(workingDir, linkURL.Host, strings.TrimPrefix(linkURL.Path, "/"))
				dirPath := path.Dir(filePath)
				err = os.MkdirAll(dirPath, os.ModePerm)
				if err != nil {
					logrus.Errorf("Failed to create directories for %s: %v", dirPath, err)
					return
				}
				resp, err := http.Get(linkURL.String())
				if err != nil {
					logrus.Errorf("Failed to download PDF %s: %v", linkURL.String(), err)
					return
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					logrus.Errorf("Failed to download PDF %s: status code %d", linkURL.String(), resp.StatusCode)
					return
				}
				file, err := os.Create(filePath)
				if err != nil {
					logrus.Errorf("Failed to create file %s: %v", filePath, err)
					return
				}
				defer file.Close()
				_, err = io.Copy(file, resp.Body)
				if err != nil {
					logrus.Errorf("Failed to save PDF to %s: %v", filePath, err)
					return
				}
			} else if linkURL.Host == "" || baseURL.Host == linkURL.Host {
				e.Request.Visit(link)
			}
		})

		err := collector.Visit(url)
		if err != nil {
			logrus.Errorf("Failed to scrape URL %s: %v", url, err)
		}

		if err := writeMetadata(metadata, metadataPath); err != nil {
			log.Fatalf("Failed to write metadata: %v", err)
		}
	}

}

func writeMetadata(metadata Metadata, path string) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
