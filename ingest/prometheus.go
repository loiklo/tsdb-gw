package ingest

import (
	"io/ioutil"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/prompb"
	"github.com/raintank/tsdb-gw/api"
	"github.com/raintank/tsdb-gw/publish"
	log "github.com/sirupsen/logrus"
	schema "gopkg.in/raintank/schema.v1"
)

func PrometheusMTWrite(ctx *api.Context) {
	if ctx.Req.Request.Body != nil {
		defer ctx.Req.Request.Body.Close()
		compressed, err := ioutil.ReadAll(ctx.Req.Request.Body)

		if err != nil {
			ctx.JSON(400, err.Error())
			log.Errorf("Read Error, %v", err)
			return
		}
		reqBuf, err := snappy.Decode(nil, compressed)
		if err != nil {
			ctx.JSON(400, err.Error())
			log.Errorf("Decode Error, %v", err)
			return
		}

		var req prompb.WriteRequest
		if err := proto.Unmarshal(reqBuf, &req); err != nil {
			ctx.JSON(400, err.Error())
			log.Errorf("Unmarshal Error, %v", err)
			return
		}

		buf := make([]*schema.MetricData, 0)
		for _, ts := range req.Timeseries {
			var name string
			var tagSet []string

			for _, l := range ts.Labels {
				if l.Name == model.MetricNameLabel {
					name = l.Value
				} else {
					tagSet = append(tagSet, l.Name+"="+l.Value)
				}
			}
			if name != "" {
				_, s := schemas.Match(name, 0)

				for _, sample := range ts.Samples {
					md := metricPool.Get()
					*md = schema.MetricData{
						Name:     name,
						Metric:   name,
						Interval: s.Retentions[0].SecondsPerPoint,
						Value:    sample.Value,
						Unit:     "unknown",
						Time:     (sample.Timestamp / 1000),
						Mtype:    "gauge",
						Tags:     tagSet,
						OrgId:    ctx.ID,
					}
					md.SetId()
					buf = append(buf, md)
				}
			} else {
				log.Errorf("prometheus metric received with empty name: %v", ts.String())
				ctx.JSON(400, "invalid metric received: __name__ label can not equal \"\"")
				return
			}
		}

		err = publish.Publish(buf)
		for _, m := range buf {
			metricPool.Put(m)
		}
		if err != nil {
			log.Errorf("failed to publish prom write metrics. %s", err)
			ctx.JSON(500, err)
			return
		}
		ctx.JSON(200, "ok")
		return
	}
	ctx.JSON(400, "no data included in request.")
}