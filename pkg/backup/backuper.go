package backup

import (
	"fmt"
	"github.com/AlexAkulov/clickhouse-backup/pkg/clickhouse"
	"github.com/AlexAkulov/clickhouse-backup/pkg/config"
	"github.com/AlexAkulov/clickhouse-backup/pkg/new_storage"
)

type Backuper struct {
	cfg             *config.Config
	ch              *clickhouse.ClickHouse
	dst             *new_storage.BackupDestination
	Version         string
	DiskToPathMap   map[string]string
	DefaultDataPath string
}

func (b *Backuper) init() error {
	var err error
	b.DefaultDataPath, err = b.ch.GetDefaultPath()
	if err != nil {
		return ErrUnknownClickhouseDataPath
	}
	disks, err := b.ch.GetDisks()
	if err != nil {
		return err
	}
	diskMap := map[string]string{}
	for _, disk := range disks {
		diskMap[disk.Name] = disk.Path
	}
	b.DiskToPathMap = diskMap
	if b.cfg.General.RemoteStorage != "none" {
		b.dst, err = new_storage.NewBackupDestination(b.cfg)
		if err != nil {
			return err
		}
		if err := b.dst.Connect(); err != nil {
			return fmt.Errorf("can't connect to %s: %v", b.dst.Kind(), err)
		}
	}
	return nil
}

func NewBackuper(cfg *config.Config) *Backuper {
	ch := &clickhouse.ClickHouse{
		Config: &cfg.ClickHouse,
	}
	return &Backuper{
		cfg: cfg,
		ch:  ch,
	}
}
