/******************************************************************************
*
*  Copyright 2019 SAP SE
*
*  Licensed under the Apache License, Version 2.0 (the "License");
*  you may not use this file except in compliance with the License.
*  You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
*  Unless required by applicable law or agreed to in writing, software
*  distributed under the License is distributed on an "AS IS" BASIS,
*  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*  See the License for the specific language governing permissions and
*  limitations under the License.
*
******************************************************************************/

package api

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/sre"
)

var (
	auditEventPublishSuccessCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "castellum_successful_auditevent_publish",
			Help: "Counter for successful audit event publish to RabbitMQ server.",
		})
	auditEventPublishFailedCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "castellum_failed_auditevent_publish",
			Help: "Counter for failed audit event publish to RabbitMQ server.",
		})
)

var (
	//taken from <https://github.com/sapcc/helm-charts/blob/20f70f7071fcc03c3cee3f053ddc7e3989a05ae8/openstack/swift/etc/statsd-exporter.yaml#L23>
	httpDurationBuckets = []float64{0.025, 0.1, 0.25, 1, 2.5}

	//1024 and 8192 indicate that the request/response probably fits inside a single
	//ethernet frame or jumboframe, respectively
	httpBodySizeBuckets = []float64{1024, 8192, 1000000, 10000000}
)

func init() {
	prometheus.MustRegister(auditEventPublishSuccessCounter)
	prometheus.MustRegister(auditEventPublishFailedCounter)

	sre.Init(sre.Config{
		AppName:                  "castellum-api",
		FirstByteDurationBuckets: httpDurationBuckets,
		ResponseDurationBuckets:  httpDurationBuckets,
		RequestBodySizeBuckets:   httpBodySizeBuckets,
		ResponseBodySizeBuckets:  httpBodySizeBuckets,
	})
}
