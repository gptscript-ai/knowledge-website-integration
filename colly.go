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

	for _, url := range metadata.Input.URLs {
		c.collector.OnHTML("body", func(e *colly.HTMLElement) {
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
				if segments[len(segments)-1] == "" {
					return
				}
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

			metadata.Output.Pages[e.Request.URL.String()] = PageDetails{
				Path:       filePath,
				URL:        e.Request.URL.String(),
				LastUpdate: time.Now().String(),
			}

			folders[path.Join(workingDir, hostname)] = struct{}{}
			metadata.Output.Folders = folders

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
				metadata.Output.Pages[linkURL.String()] = PageDetails{
					Path:       filePath,
					URL:        linkURL.String(),
					LastUpdate: time.Now().String(),
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
	for url, page := range metadata.Output.Pages {
		if !visited[url] {
			if err := os.RemoveAll(page.Path); err != nil {
				logrus.Errorf("Failed to remove %s: %v", page.Path, err)
			}
			delete(metadata.Output.Pages, url)
		}
	}

	for folder := range metadata.Output.Folders {
		if _, ok := folders[folder]; !ok {
			logrus.Infof("removing folder %s", folder)
			if err := os.RemoveAll(folder); err != nil {
				logrus.Errorf("Failed to remove %s: %v", folder, err)
			}
			delete(metadata.Output.Folders, folder)
		}
	}

	metadata.Output.Status = ""
	metadata.Output.Error = ""
	if err := writeMetadata(metadata, metadataPath); err != nil {
		logrus.Fatalf("Failed to write metadata: %v", err)
	}
}
