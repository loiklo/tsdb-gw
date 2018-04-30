package gcp

import (
	"context"
	"flag"
	"strconv"

	"cloud.google.com/go/bigtable"
	"github.com/raintank/tsdb-gw/persister/storage"
	"github.com/sirupsen/logrus"
	schema "gopkg.in/raintank/schema.v1"
)

// Config for a StorageClient
type Config struct {
	project   string
	instance  string
	tablename string
}

// RegisterFlags adds the flags required to config this to the given FlagSet
func (cfg *Config) RegisterFlags(f *flag.FlagSet) {
	f.StringVar(&cfg.project, "bigtable-project", "persister", "Bigtable project ID.")
	f.StringVar(&cfg.instance, "bigtable-instance", "persister", "Bigtable instance ID.")
	f.StringVar(&cfg.tablename, "bigtable-tablename", "persister", "Bigtable table name f.")
}

// storageClient implements storage.Storage for GCP.
type storageClient struct {
	cfg       Config
	client    *bigtable.Client
	tablename string
}

// NewStorageClient returns a new StorageClient.
func NewStorageClient(ctx context.Context, cfg Config) (storage.Storage, error) {
	adminClient, err := bigtable.NewAdminClient(ctx, cfg.project, cfg.instance)
	if err != nil {
		return nil, err
	}

	tables, err := adminClient.Tables(context.Background())
	if err != nil {
		logrus.Fatalf("Could not fetch table list: %v", err)
	}

	if !sliceContains(tables, cfg.tablename) {
		logrus.Printf("Creating table %s", cfg.tablename)
		if err := adminClient.CreateTable(context.Background(), cfg.tablename); err != nil {
			logrus.Fatalf("Could not create table %s: %v", cfg.tablename, err)
		}
	}

	tblInfo, err := adminClient.TableInfo(context.Background(), cfg.tablename)
	if err != nil {
		logrus.Fatalf("Could not read info for table %s: %v", cfg.tablename, err)
	}

	if !sliceContains(tblInfo.Families, "metrics") {
		if err := adminClient.CreateColumnFamily(context.Background(), cfg.tablename, "metrics"); err != nil {
			logrus.Fatalf("Could not create column family %s: %v", "metrics", err)
		}
	}

	client, err := bigtable.NewClient(ctx, cfg.project, cfg.instance)
	if err != nil {
		return nil, err
	}

	return &storageClient{
		cfg:       cfg,
		client:    client,
		tablename: cfg.tablename,
	}, nil
}

func (s *storageClient) Store(metrics []*schema.MetricData) error {
	table := s.client.Open(s.tablename)
	var data []byte
	rowKeys := make([]string, 0, len(metrics))
	muts := make([]*bigtable.Mutation, 0, len(metrics))
	for _, m := range metrics {
		msg, err := m.MarshalMsg(data)
		if err != nil {
			logrus.Errorf("unable to marshal metric: %v", m.Id)
			return err
		}
		mut := bigtable.NewMutation()
		mut.Set("metrics", "metricdata", bigtable.Now(), msg)
		muts = append(muts, mut)

		m.SetId()
		rowKeys = append(rowKeys, m.Id)
	}

	if len(muts) > 0 {
		errs, err := table.ApplyBulk(context.Background(), rowKeys, muts)
		if err != nil {
			return err
		}
		if len(errs) > 0 {
			for _, e := range errs {
				logrus.Error(e)
			}
		}
	}

	return nil
}
func (s *storageClient) Retrieve(orgID int) ([]*schema.MetricData, error) {
	tbl := s.client.Open(s.tablename)
	rr := bigtable.PrefixRange(strconv.Itoa(orgID))
	var metrics []*schema.MetricData
	err := tbl.ReadRows(context.Background(), rr, func(r bigtable.Row) bool {
		m := &schema.MetricData{}
		_, err := m.UnmarshalMsg(r["metrics"][0].Value)
		if err != nil {
			logrus.Errorf("unable to decode metric from row %v", r.Key())
			return false
		}
		metrics = append(metrics, m)
		return true
	}, bigtable.RowFilter(bigtable.FamilyFilter("metrics")))

	if err != nil {
		return nil, err
	}

	return metrics, nil
}

func sliceContains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
