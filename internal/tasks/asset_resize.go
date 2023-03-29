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

package tasks

import (
	"fmt"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/sqlext"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

// WARNING: This must be run in a transaction, or else `FOR UPDATE SKIP LOCKED`
// will not work as expected.
var selectAndDeleteNextResizeQuery = sqlext.SimplifyWhitespace(`
	DELETE FROM pending_operations WHERE id = (
		SELECT id FROM pending_operations WHERE greenlit_at < $1 AND (retry_at IS NULL OR retry_at < $1)
		ORDER BY reason ASC LIMIT 1
		-- prevent other job loops from working on the same asset concurrently
		FOR UPDATE SKIP LOCKED
	) RETURNING *
`)

const (
	maxRetries    = 3
	retryInterval = 2 * time.Minute
)

// AssetScrapingJob returns a job where each task is a asset that needs to be
// scraped. The task checks its status and creates/confirms/cancels operations accordingly.
func (c *Context) AssetResizingJob(registerer prometheus.Registerer) jobloop.Job {
	return (&jobloop.TxGuardedJob[*gorp.Transaction, db.PendingOperation]{
		Metadata: jobloop.JobMetadata{
			ReadableName:    "asset resizing",
			ConcurrencySafe: true, //because "FOR UPDATE SKIP LOCKED" is used
			CounterOpts: prometheus.CounterOpts{
				Name: "castellum_asset_resizes",
				Help: "Counter for asset resize operations.",
			},
			CounterLabels: []string{"asset_type"},
		},
		BeginTx:     c.DB.Begin,
		DiscoverRow: c.discoverAssetResize,
		ProcessRow:  c.processAssetResize,
	}).Setup(registerer)
}

func (c *Context) discoverAssetResize(tx *gorp.Transaction, labels prometheus.Labels) (op db.PendingOperation, err error) {
	err = tx.SelectOne(&op, selectAndDeleteNextResizeQuery, c.TimeNow())
	return op, err
}

func (c *Context) processAssetResize(tx *gorp.Transaction, op db.PendingOperation, labels prometheus.Labels) error {
	//find the corresponding asset, resource and asset manager
	var asset db.Asset
	err := tx.SelectOne(&asset, `SELECT * FROM assets WHERE id = $1`, op.AssetID)
	if err != nil {
		return err
	}

	var res db.Resource
	err = tx.SelectOne(&res, `SELECT * FROM resources WHERE id = $1`, asset.ResourceID)
	if err != nil {
		return err
	}
	labels["asset_type"] = string(res.AssetType)

	manager, _ := c.Team.ForAssetType(res.AssetType)
	if manager == nil {
		return fmt.Errorf("no asset manager for asset type %q", res.AssetType)
	}

	//perform the resize operation (we give asset.Size instead of op.OldSize
	//since this is the most up-to-date asset size that we have)
	outcome, err := manager.SetAssetSize(res, asset.UUID, asset.Size, op.NewSize)
	errorMessage := ""
	if err != nil {
		logg.Error("cannot resize %s %s to size %d: %s", string(res.AssetType), asset.UUID, op.NewSize, err.Error())
		errorMessage = err.Error()
	}

	//if we have not exceeded our retry budget, put this operation back in the queue
	//
	//We only do this for outcome "errored", which indicates a system error.
	//These problems are usually discovered via alerts and quickly resolved, so
	//there is actual hope that everything will be better in a few minutes
	//without us failing the entire operation. For outcome "failed", we have a
	//user error and the user will likely only notice once they see the failed
	//operation in Castellum, so there is little use retrying here.
	if outcome == castellum.OperationOutcomeErrored && op.ErroredAttempts < maxRetries {
		op.ID = 0
		op.ErroredAttempts++
		retryAt := c.TimeNow().Add(retryInterval)
		op.RetryAt = &retryAt

		err = tx.Insert(&op)
		if err != nil {
			return err
		}
		return tx.Commit()
	}

	finishedOp := op.IntoFinishedOperation(outcome, c.TimeNow())
	finishedOp.ErrorMessage = errorMessage
	err = tx.Insert(&finishedOp)
	if err != nil {
		return err
	}

	//mark asset as having just completed as resize operation (see
	//logic in ScrapeNextAsset() for details)
	if outcome == castellum.OperationOutcomeSucceeded {
		_, err := tx.Exec(`UPDATE assets SET expected_size = $1 WHERE id = $2`,
			finishedOp.NewSize, asset.ID)
		if err != nil {
			return err
		}
	}

	core.CountStateTransition(res, asset.UUID, castellum.OperationStateGreenlit, finishedOp.State())
	return tx.Commit()
}
