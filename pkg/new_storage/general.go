package new_storage

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AlexAkulov/clickhouse-backup/config"
	"github.com/AlexAkulov/clickhouse-backup/internal/progressbar"

	"github.com/mholt/archiver"
	"gopkg.in/djherbis/buffer.v1"
	"gopkg.in/djherbis/nio.v2"
)

const (
	// MetaFileName - meta file name
	MetaFileName = "meta.json"
	// BufferSize - size of ring buffer between stream handlers
	BufferSize = 4 * 1024 * 1024
)

type Backup struct {
	Name string
	Size int64
	Date time.Time
}

// MetaFile - structure describe meta file that will be added to incremental backups archive.
// Contains info of required files in backup and files
type MetaFile struct {
	RequiredBackup string   `json:"required_backup"`
	Hardlinks      []string `json:"hardlinks"`
}

type BackupDestination struct {
	RemoteStorage
	path               string
	compressionFormat  string
	compressionLevel   int
	disableProgressBar bool
	backupsToKeep      int
}

func (bd *BackupDestination) RemoveOldBackups(keep int) error {
	if keep < 1 {
		return nil
	}
	backupList, err := bd.BackupList()
	if err != nil {
		return err
	}
	backupsToDelete := GetBackupsToDelete(backupList, keep)
	for _, backupToDelete := range backupsToDelete {
		if err := bd.RemoveBackup(backupToDelete.Name); err != nil {
			return err
		}
	}
	return nil
}

func (bd *BackupDestination) RemoveBackup(backupName string) error {
	objects := []string{}
	if err := bd.Walk(bd.path, func(f RemoteFile) {
		if strings.HasPrefix(f.Name(), path.Join(bd.path, backupName)) {
			objects = append(objects, f.Name())
		}
	}); err != nil {
		return err
	}
	for _, key := range objects {
		err := bd.DeleteFile(key)
		if err != nil {
			return err
		}
	}
	return nil
}

func (bd *BackupDestination) BackupsToKeep() int {
	return bd.backupsToKeep
}

func (bd *BackupDestination) BackupList() ([]Backup, error) {
	type ClickhouseBackup struct {
		Metadata bool
		Shadow   bool
		Tar      bool
		Size     int64
		Date     time.Time
	}
	files := map[string]ClickhouseBackup{}
	path := bd.path
	err := bd.Walk(path, func(o RemoteFile) {
		if strings.HasPrefix(o.Name(), path) {
			key := strings.TrimPrefix(o.Name(), path)
			key = strings.TrimPrefix(key, "/")
			parts := strings.Split(key, "/")

			if strings.HasSuffix(parts[0], ".tar") ||
				strings.HasSuffix(parts[0], ".tar.lz4") ||
				strings.HasSuffix(parts[0], ".tar.bz2") ||
				strings.HasSuffix(parts[0], ".tar.gz") ||
				strings.HasSuffix(parts[0], ".tar.sz") ||
				strings.HasSuffix(parts[0], ".tar.xz") {
				files[parts[0]] = ClickhouseBackup{
					Tar:  true,
					Date: o.LastModified(),
					Size: o.Size(),
				}
			}

			if len(parts) > 1 {
				b := files[parts[0]]
				files[parts[0]] = ClickhouseBackup{
					Metadata: b.Metadata || parts[1] == "metadata",
					Shadow:   b.Shadow || parts[1] == "shadow",
					Date:     b.Date,
					Size:     b.Size,
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}
	result := []Backup{}
	for name, e := range files {
		if e.Metadata && e.Shadow || e.Tar {
			result = append(result, Backup{
				Name: name,
				Date: e.Date,
				Size: e.Size,
			})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Date.Before(result[j].Date)
	})
	return result, nil
}

func (bd *BackupDestination) CompressedStreamDownload(remotePath string, localPath string) error {
	if err := os.MkdirAll(localPath, os.ModePerm); err != nil {
		return err
	}
	archiveName := path.Join(bd.path, fmt.Sprintf("%s.%s", remotePath, getExtension(bd.compressionFormat)))
	if err := bd.Connect(); err != nil {
		return err
	}

	// get this first as GetFileReader blocks the ftp control channel
	file, err := bd.GetFile(archiveName)
	if err != nil {
		return err
	}
	filesize := file.Size()

	reader, err := bd.GetFileReader(archiveName)
	if err != nil {
		return err
	}
	defer reader.Close()

	bar := progressbar.StartNewByteBar(!bd.disableProgressBar, filesize)
	buf := buffer.New(BufferSize)
	bufReader := nio.NewReader(reader, buf)
	proxyReader := bar.NewProxyReader(bufReader)
	z, _ := getArchiveReader(bd.compressionFormat)
	if err := z.Open(proxyReader, 0); err != nil {
		return err
	}
	defer z.Close()
	var metafile MetaFile
	for {
		file, err := z.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		header, ok := file.Header.(*tar.Header)
		if !ok {
			return fmt.Errorf("expected header to be *tar.Header but was %T", file.Header)
		}
		if header.Name == MetaFileName {
			b, err := ioutil.ReadAll(file)
			if err != nil {
				return fmt.Errorf("can't read %s", MetaFileName)
			}
			if err := json.Unmarshal(b, &metafile); err != nil {
				return err
			}
			continue
		}
		extractFile := filepath.Join(localPath, header.Name)
		extractDir := filepath.Dir(extractFile)
		if _, err := os.Stat(extractDir); os.IsNotExist(err) {
			os.MkdirAll(extractDir, os.ModePerm)
		}
		dst, err := os.Create(extractFile)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, file); err != nil {
			return err
		}
		if err := dst.Close(); err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	if metafile.RequiredBackup != "" {
		log.Printf("Backup '%s' required '%s'. Downloading.", remotePath, metafile.RequiredBackup)
		err := bd.CompressedStreamDownload(metafile.RequiredBackup, filepath.Join(filepath.Dir(localPath), metafile.RequiredBackup))
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("can't download '%s': %v", metafile.RequiredBackup, err)
		}
	}
	for _, hardlink := range metafile.Hardlinks {
		newname := filepath.Join(localPath, hardlink)
		extractDir := filepath.Dir(newname)
		oldname := filepath.Join(filepath.Dir(localPath), metafile.RequiredBackup, hardlink)
		if _, err := os.Stat(extractDir); os.IsNotExist(err) {
			os.MkdirAll(extractDir, os.ModePerm)
		}
		if err := os.Link(oldname, newname); err != nil {
			return err
		}
	}
	bar.Finish()
	return nil
}

func (bd *BackupDestination) CompressedStreamUpload(baseLocalPath string, files []string, remotePath string) error {
	if _, err := bd.GetFile(remotePath); err != nil {
		if err != ErrNotFound {
			return err
		}
	}

	var totalBytes int64
	for _, filename := range files {
		finfo, err := os.Stat(path.Join(baseLocalPath, filename))
		if err != nil {
			return err
		}
		if finfo.Mode().IsRegular() {
			totalBytes += finfo.Size()
		}
	}
	bar := progressbar.StartNewByteBar(!bd.disableProgressBar, totalBytes)
	defer bar.Finish()
	buf := buffer.New(BufferSize)
	body, w := nio.Pipe(buf)
	go func() (err error) {
		defer w.CloseWithError(err)
		iobuf := buffer.New(BufferSize)
		z, _ := getArchiveWriter(bd.compressionFormat, bd.compressionLevel)
		if err = z.Create(w); err != nil {
			return
		}
		defer z.Close()
		for _, f := range files {
			filePath := path.Join(baseLocalPath, f)
			info, err := os.Stat(filePath)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			bar.Add64(info.Size())
			file, err := os.Open(filePath)
			if err != nil {
				return err
			}
			defer file.Close()
			bfile := nio.NewReader(file, iobuf)
			defer bfile.Close()
			if err :=  z.Write(archiver.File{
				FileInfo: archiver.FileInfo{
					FileInfo:   info,
					CustomName: f,
				},
				ReadCloser: bfile,
			}); err != nil {
				return err
			}
		}
		return
	}()

	if err := bd.PutFile(remotePath, body); err != nil {
		return err
	}
	return nil
}

func NewBackupDestination(cfg config.Config) (*BackupDestination, error) {
	switch cfg.General.RemoteStorage {
	case "s3":
		s3Storage := &S3{Config: &cfg.S3}
		return &BackupDestination{
			s3Storage,
			cfg.S3.Path,
			cfg.S3.CompressionFormat,
			cfg.S3.CompressionLevel,
			cfg.General.DisableProgressBar,
			cfg.General.BackupsToKeepRemote,
		}, nil
	default:
		return nil, fmt.Errorf("storage type '%s' not supported", cfg.General.RemoteStorage)
	}
}