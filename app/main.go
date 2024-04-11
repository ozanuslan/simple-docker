package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	image := os.Args[2]
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	chroot := path.Join(os.TempDir(), fmt.Sprintf("%d", os.Getpid()))
	r := NewRegistry()
	imagePath, err := r.PullImage(image)
	if err != nil {
		panic(err)
	}
	CopyDir(chroot, imagePath)

	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Chroot:     chroot,
		Cloneflags: syscall.CLONE_NEWPID,
	}
	err = cmd.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v", err)
		os.Exit(cmd.ProcessState.ExitCode())
	}
}

const (
	registryBaseURL     = "registry.hub.docker.com"
	registryAuthBaseURL = "auth.docker.io"
)

type Registry struct {
	baseURL          string
	authURL          string
	authToken        string
	imageDownloadDir string
}

func NewRegistry() *Registry {
	return &Registry{baseURL: registryBaseURL, authURL: registryAuthBaseURL, imageDownloadDir: path.Join(os.TempDir(), "container-images")}
}

func (r *Registry) PullImage(image string) (string, error) {
	split := strings.Split(image, ":")
	imageName := split[0]
	// tag := "latest"
	if len(split) >= 2 {
		// tag = split[1]
	}

	r.auth(imageName)
	// indexManifest, err := r.getIndexManifest(imageName, tag)
	// if err != nil {
	// 	return "", err
	// }
	digest := "latest"
	mediaType := "application/vnd.docker.distribution.manifest.v2+json"

	imageManifest := r.getLayerManifest(imageName, digest, mediaType)

	path, err := r.downloadImage(imageName, imageManifest)
	if err != nil {
		return "", err
	}

	return path, nil
}

func (r *Registry) getLayerManifest(imageName string, indexDigest string, mediaType string) ImageManifest {
	reqUrl := fmt.Sprintf("https://%s/v2/library/%s/manifests/%s", r.baseURL, imageName, indexDigest)
	req, err := http.NewRequest("GET", reqUrl, nil)
	if err != nil {
		panic(err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.authToken))
	req.Header.Set("Accept", mediaType)

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var imageManifest ImageManifest
	err = json.Unmarshal(body, &imageManifest)
	if err != nil {
		panic(err)
	}

	return imageManifest
}

type ImageManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		MediaType string `json:"mediaType"`
		Size      uint   `json:"size"`
		Digest    string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Size      uint   `json:"size"`
		Digest    string `json:"digest"`
	} `json:"layers"`
}

type IndexManifest struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Platform  struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		Variant      string `json:"variant"`
	} `json:"platform"`
	Size uint32 `json:"size"`
}

type IndexManifestBody struct {
	Manifests     []IndexManifest `json:"manifests"`
	MediaType     string          `json:"mediaType"`
	SchemaVersion int             `json:"schemaVersion"`
}

type AuthBody struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

func (r *Registry) auth(imageName string) error {
	scope := fmt.Sprintf("repository:library/%s:pull", imageName)
	reqUrl := fmt.Sprintf("https://%s/token?service=registry.docker.io&scope=%s", r.authURL, scope)
	res, err := http.Get(reqUrl)
	if err != nil {
		return err
	}

	var authBody AuthBody
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	err = json.Unmarshal(body, &authBody)
	if err != nil {
		return err
	}
	r.authToken = authBody.Token

	return nil
}

func (r *Registry) getIndexManifest(imageName string, tag string) (IndexManifest, error) {
	reqUrl := fmt.Sprintf("https://%s/v2/library/%s/manifests/%s", r.baseURL, imageName, tag)
	req, err := http.NewRequest("GET", reqUrl, nil)
	if err != nil {
		return IndexManifest{}, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.authToken))
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return IndexManifest{}, err
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return IndexManifest{}, err
	}
	var manifestBody IndexManifestBody
	err = json.Unmarshal(body, &manifestBody)
	if err != nil {
		return IndexManifest{}, err
	}

	os, arch := runtime.GOOS, runtime.GOARCH
	for _, manifest := range manifestBody.Manifests {
		platform := manifest.Platform
		if os == platform.OS && arch == platform.Architecture {
			return manifest, nil
		}
	}

	return IndexManifest{}, fmt.Errorf("no matching os/arch image")
}

func (r *Registry) downloadImage(imageName string, imageManifest ImageManifest) (string, error) {
	imgDir := path.Join(r.imageDownloadDir, imageName)
	err := os.MkdirAll(imgDir, 0755)
	if err != nil {
		return "", err
	}
	for _, layer := range imageManifest.Layers {
		reqUrl := fmt.Sprintf("https://%s/v2/library/%s/blobs/%s", r.baseURL, imageName, layer.Digest)
		req, err := http.NewRequest("GET", reqUrl, nil)
		if err != nil {
			return "", err
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.authToken))
		req.Header.Set("Accept", layer.MediaType)

		client := &http.Client{}
		res, err := client.Do(req)
		if err != nil {
			return "", err
		}

		gzr, err := gzip.NewReader(res.Body)
		if err != nil {
			return "", err
		}
		defer gzr.Close()

		tr := tar.NewReader(gzr)

		for {
			h, err := tr.Next()
			if err == io.EOF {
				break // finished reading archive
			}
			if err != nil {
				return "", nil
			}
			if h == nil {
				break
			}

			targetPath := path.Join(imgDir, h.Name)
			switch h.Typeflag {
			case tar.TypeDir:
				err = os.MkdirAll(targetPath, os.FileMode(h.Mode))
				if err != nil {
					return "", err
				}
			case tar.TypeReg:
				fd, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY, os.FileMode(h.Mode))
				if err != nil {
					return "", err
				}

				_, err = io.Copy(fd, tr)
				if err != nil {
					return "", err
				}

				fd.Close()
			case tar.TypeSymlink:
				err := os.Symlink(h.Linkname, targetPath)
				if err != nil {
					return "", err
				}
			}
		}
	}
	return imgDir, nil
}

func CopyDir(dst string, src string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// copy to this path
		outpath := filepath.Join(dst, strings.TrimPrefix(path, src))

		if info.IsDir() {
			os.MkdirAll(outpath, info.Mode())
			return nil // means recursive
		}

		// handle irregular files
		if !info.Mode().IsRegular() {
			switch info.Mode().Type() & os.ModeType {
			case os.ModeSymlink:
				link, err := os.Readlink(path)
				if err != nil {
					return err
				}
				return os.Symlink(link, outpath)
			}
			return nil
		}

		// copy contents of regular file efficiently

		// open input
		in, _ := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()

		// create output
		fh, err := os.Create(outpath)
		if err != nil {
			return err
		}
		defer fh.Close()

		// make it the same
		fh.Chmod(info.Mode())

		// copy content
		_, err = io.Copy(fh, in)
		return err
	})
}
