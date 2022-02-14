package new_storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/AlexAkulov/clickhouse-backup/pkg/config"
	"github.com/ncw/swift/v2"
)

// SWIFT - presents methods for manipulate data on SWIFT
type SWIFT struct {
	connection swift.Connection
	Config     *config.SWIFTConfig
	Debug      bool
	ctx        context.Context
}

// Connect - connect to s3
func (s *SWIFT) Connect() error {
	s.connection = swift.Connection{
		UserName: s.Config.UserName,
		ApiKey:   s.Config.Password,
		AuthUrl:  s.Config.AuthUrl,
		Domain:   s.Config.Domain,
		Tenant:   s.Config.Tenant,
	}
	s.ctx = context.Background()
	err := s.connection.Authenticate(s.ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *SWIFT) Kind() string {
	return "SWIFT"
}

func (s *SWIFT) GetFileReader(key string) (io.ReadCloser, error) {
	fmt.Println("getfilereader")
	content, err := s.connection.ObjectGetBytes(s.ctx, s.Config.Container, key)
	return io.NopCloser(bytes.NewReader(content)), err
}

func (s *SWIFT) PutFile(key string, r io.ReadCloser) error {
	fmt.Println("put")
	_, err := s.connection.ObjectPut(s.ctx, s.Config.Container, key, r, true, "", "nil", nil)
	return err
}

func (s *SWIFT) DeleteFile(key string) error {
	fmt.Println("delete")
	err := s.connection.ObjectDelete(s.ctx, s.Config.Container, key)
	return err
}

func (s *SWIFT) StatFile(key string) (RemoteFile, error) {
	fmt.Println("stat file ", key)
	info, _, err := s.connection.Object(s.ctx, s.Config.Container, key)
	if err != nil {
		return nil, err
	}
	return &swiftFile{
		size:         info.Bytes,
		lastModified: info.LastModified,
		name:         info.Name,
	}, nil
}

func (s *SWIFT) Walk(swiftPath string, recursive bool, process func(RemoteFile) error) error {
	fmt.Println("walk")
	objects, err := s.connection.ObjectsAll(s.ctx, s.Config.Container, new(swift.ObjectsOpts))
	if err != nil {
		return err
	}
	swiftFiles := map[string]*swiftFile{}
	for _, object := range objects {
		swiftFiles[strings.Split(object.Name, "/")[0]] = &swiftFile{
			size:         object.Bytes,
			lastModified: object.LastModified,
			name:         strings.Split(object.Name, "/")[0],
		}
	}

	for _, swiftFile := range swiftFiles {
		fmt.Println("File: ", swiftFile.name)
		process(swiftFile)
	}
	return nil
}

type swiftFile struct {
	size         int64
	lastModified time.Time
	name         string
}

func (f *swiftFile) Size() int64 {
	return f.size
}

func (f *swiftFile) LastModified() time.Time {
	return f.lastModified
}

func (f *swiftFile) Name() string {
	return f.name
}
