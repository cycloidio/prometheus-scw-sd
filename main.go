// Copyright 2018 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/scaleway/prometheus-scw-sd/model"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	api "github.com/scaleway/go-scaleway"
	scw "github.com/scaleway/go-scaleway/types"
	"github.com/scaleway/prometheus-scw-sd/adapter"
	"github.com/scaleway/prometheus-scw-sd/targetgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	a          = kingpin.New("sd adapter usage", "Tool to generate file_sd target files for unimplemented SD mechanisms.")
	token      = a.Flag("token", "The token for Scaleway API.").Required().String()
	private    = a.Flag("private", "Use servers private IP.").Bool()
	outputFile = a.Flag("output.file", "Output file for file_sd compatible file.").Default("scw_sd.json").String()
	port       = a.Flag("port", "Port on which to scrape metrics.").Default("9100").Int()
	interval   = a.Flag("time.interval", "Time in second to wait between each refresh.").Default("90").Int()
	logger     log.Logger

	scwPrefix = model.MetaLabelPrefix + "scw_"
	// archLabel is the name for the label containing the server's architecture.
	archLabel = scwPrefix + "architecture"
	// commercialTypeLabel is the name for the label containing the server's commercial type.
	commercialTypeLabel = scwPrefix + "commercial_type"
	// identifierLabel is the name for the label containing the server's identifier.
	identifierLabel = scwPrefix + "identifier"
	// nodeLabel is the name for the label containing the server's name.
	nameLabel = scwPrefix + "name"
	// imageIDLabel is the name for the label containing the server's image ID.
	imageIDLabel = scwPrefix + "image_id"
	// imageNameLabel is the name for the label containing the server's image name.
	imageNameLabel = scwPrefix + "image_name"
	// orgLabel is the name for the label containing the server's organization.
	orgLabel = scwPrefix + "organization"
	// privateIPLabel is the name for the label containing the server's private IP.
	privateIPLabel = scwPrefix + "private_ip"
	// publicIPLabel is the name for the label containing the server's public IP.
	publicIPLabel = scwPrefix + "public_ip"
	// stateLabel is the name for the label containing the server's state.
	stateLabel = scwPrefix + "state"
	// tagsLabel is the name for the label containing all the server's tags.
	tagsLabel = scwPrefix + "tags"
	// platformLabel is the name for the label containing all the server's platform location.
	platformLabel = scwPrefix + "platform_id"
	// hypervisorLabel is the name for the label containing all the server's hypervisor location.
	hypervisorLabel = scwPrefix + "hypervisor_id"
	// nodeLabel is the name for the label containing all the server's node location.
	nodeLabel = scwPrefix + "node_id"
	// bladeLabel is the name for the label containing all the server's blade location.
	bladeLabel = scwPrefix + "blade_id"
	// chassisLabel is the name for the label containing all the server's chassis location.
	chassisLabel = scwPrefix + "chassis_id"
	// clusterLabel is the name for the label containing all the server's cluster location.
	clusterLabel = scwPrefix + "cluster_id"
	// zoneLabel is the name for the label containing all the server's zone location.
	zoneLabel = scwPrefix + "zone_id"
)

// Note: create a config struct for Scaleway SD type here.
type sdConfig struct {
	Token           string
	TagSeparator    string
	RefreshInterval int
}

// Discovery retrieves targets information from Scaleway API.
type discovery struct {
	client          *api.ScalewayAPI
	refreshInterval int
	scrapePort      int
	tagSeparator    string
	logger          log.Logger
}

func (d *discovery) scalewayTags(tags []string) string {
	var scwTags string
	// We surround the separated list with the separator as well. This way regular expressions
	// in relabeling rules don't have to consider tag positions.
	if len(tags) > 0 {
		sort.Strings(tags)
		scwTags = d.tagSeparator + strings.Join(tags, d.tagSeparator) + d.tagSeparator
	}
	return scwTags
}

func (d *discovery) scalewayAddress(server scw.ScalewayServer) string {
	if *private {
		return net.JoinHostPort(server.PrivateIP, fmt.Sprintf("%d", d.scrapePort))
	}
	return net.JoinHostPort(server.PublicAddress.IP, fmt.Sprintf("%d", d.scrapePort))
}

func (d *discovery) appendScalewayServer(tgs []*targetgroup.Group, server scw.ScalewayServer) []*targetgroup.Group {
	addr := d.scalewayAddress(server)
	tags := d.scalewayTags(server.Tags)
	target := model.LabelSet{model.AddressLabel: model.LabelValue(addr)}
	labels := model.LabelSet{
		model.LabelName(archLabel): model.LabelValue(server.Arch),
		model.LabelName(tagsLabel): model.LabelValue(tags),
		model.LabelName(zoneLabel): model.LabelValue(server.Location.ZoneID),
	}
	for i := range tgs {
		if reflect.DeepEqual(tgs[i].Labels, labels) {
			tgs[i].Targets = append(tgs[i].Targets, target)
			return tgs
		}
	}
	tgroup := targetgroup.Group{
		Source: server.Name,
		Labels: make(model.LabelSet),
	}
	tgroup.Labels = labels
	tgroup.Targets = make([]model.LabelSet, 0, 1)
	tgroup.Targets = append(tgroup.Targets, target)
	tgs = append(tgs, &tgroup)
	return tgs
}

func (d *discovery) Run(ctx context.Context, ch chan<- []*targetgroup.Group) {
	for c := time.Tick(time.Duration(d.refreshInterval) * time.Second); ; {
		srvs, err := d.client.GetServers(true, 0)
		if err != nil {
			level.Error(d.logger).Log("msg", "Error retreiving server list", "err", err)
			time.Sleep(time.Duration(d.refreshInterval) * time.Second)
			continue
		}

		var tgs []*targetgroup.Group
		for _, srv := range *srvs {
			level.Info(d.logger).Log("msg", fmt.Sprintf("Server found: %s", srv.Name))
			tgs = d.appendScalewayServer(tgs, srv)
		}

		if err == nil {
			// We're returning all Scaleway services as a single targetgroup.
			ch <- tgs
		}
		// Wait for ticker or exit when ctx is closed.
		select {
		case <-c:
			continue
		case <-ctx.Done():
			return
		}
	}
}

func main() {
	a.HelpFlag.Short('h')

	_, err := a.Parse(os.Args[1:])
	if err != nil {
		fmt.Println("err: ", err)
		return
	}
	logger = log.NewSyncLogger(log.NewLogfmtLogger(os.Stdout))
	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
	client, err := api.NewScalewayAPI("", *token, "", "")
	if err != nil {
		fmt.Println("Error creating Scaleway API client, err:", err)
		return
	}
	ctx := context.Background()
	disc := &discovery{
		client:          client,
		refreshInterval: *interval,
		scrapePort:      *port,
		tagSeparator:    ",",
		logger:          logger,
	}
	sdAdapter := adapter.NewAdapter(ctx, *outputFile, "ScalewaySD", disc, logger)
	sdAdapter.Run()

	<-ctx.Done()
}
