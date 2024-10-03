package main

import (
	"io"
	"net/http"
	url2 "net/url"
	"os"
	"path"
	"strings"
	"time"

	"fmt"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/gocolly/colly"
	"github.com/sirupsen/logrus"
)

type Colly struct {
	collector *colly.Collector
}

func NewColly() *Colly {
	return &Colly{
		collector: colly.NewCollector(),
	}
}

func (c *Colly) Crawl(metadata *Metadata, metadataPath string, workingDir string) {
	converter := md.NewConverter("", true, nil)

	visited := make(map[string]bool)
	folders := make(map[string]struct{})
	exclude := make(map[string]bool)

	for _, url := range metadata.Input.Exclude {
		exclude[url] = true
	}

	for _, url := range metadata.Input.WebsiteCrawlingConfig.URLs {
		c.collector.OnHTML("body", func(e *colly.HTMLElement) {
			if visited[e.Request.URL.String()] {
				return
			}
			if exclude[e.Request.URL.String()] {
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
				if trimmedPath == "" {
					filePath = path.Join(workingDir, hostname, "index.md")
				} else {
					segments := strings.Split(trimmedPath, "/")
					fileName := segments[len(segments)-1] + ".md"
					filePath = path.Join(path.Join(workingDir, hostname, strings.Join(segments[:len(segments)-1], "/")), fileName)
				}
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

			metadata.Output.Files[e.Request.URL.String()] = FileDetails{
				FilePath:  filePath,
				URL:       e.Request.URL.String(),
				UpdatedAt: time.Now().String(),
			}

			folders[path.Join(workingDir, hostname)] = struct{}{}
			metadata.Output.State.WebsiteCrawlingState.Folders = folders

			metadata.Output.Status = fmt.Sprintf("scraped %d pages", len(visited))
			if err := writeMetadata(metadata, metadataPath); err != nil {
				logrus.Fatalf("Failed to write metadata: %v", err)
			}
		})

		c.collector.OnHTML("a[href]", func(e *colly.HTMLElement) {
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
			if visited[linkURL.String()] {
				return
			}
			if strings.ToLower(path.Ext(linkURL.Path)) == ".pdf" {
				logrus.Infof("downloading PDF %s", linkURL.String())
				if exclude[linkURL.String()] {
					return
				}
				filePath := path.Join(workingDir, baseURL.Host, linkURL.Host, strings.TrimPrefix(linkURL.Path, "/"))
				dirPath := path.Dir(filePath)
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

				err = os.MkdirAll(dirPath, os.ModePerm)
				if err != nil {
					logrus.Errorf("Failed to create directories for %s: %v", dirPath, err)
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
				visited[linkURL.String()] = true

				metadata.Output.Status = fmt.Sprintf("scraped %d pages", len(visited))
				metadata.Output.Files[linkURL.String()] = FileDetails{
					FilePath:  filePath,
					URL:       linkURL.String(),
					UpdatedAt: time.Now().String(),
				}

				if err := writeMetadata(metadata, metadataPath); err != nil {
					logrus.Fatalf("Failed to write metadata: %v", err)
				}

			} else if linkURL.Host == "" || baseURL.Host == linkURL.Host {
				e.Request.Visit(link)
			}
		})

		err := c.collector.Visit(url)
		if err != nil {
			logrus.Errorf("Failed to visit %s: %v", url, err)
			metadata.Output.Error = err.Error()
		}
	}
	for url, file := range metadata.Output.Files {
		if !visited[url] || exclude[url] {
			logrus.Infof("removing file %s", file.FilePath)
			if err := os.RemoveAll(file.FilePath); err != nil {
				logrus.Errorf("Failed to remove %s: %v", file.FilePath, err)
			}
			delete(metadata.Output.Files, url)
		}
	}

	for folder := range metadata.Output.State.WebsiteCrawlingState.Folders {
		if _, ok := folders[folder]; !ok {
			logrus.Infof("removing folder %s", folder)
			if err := os.RemoveAll(folder); err != nil {
				logrus.Errorf("Failed to remove %s: %v", folder, err)
			}
			delete(metadata.Output.State.WebsiteCrawlingState.Folders, folder)
		}
	}

	metadata.Output.Status = ""
	metadata.Output.Error = ""
	if err := writeMetadata(metadata, metadataPath); err != nil {
		logrus.Fatalf("Failed to write metadata: %v", err)
	}
}
