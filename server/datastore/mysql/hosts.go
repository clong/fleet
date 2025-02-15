package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/fleetdm/fleet/v4/server/fleet"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

var hostSearchColumns = []string{"hostname", "uuid", "hardware_serial", "primary_ip"}

func (d *Datastore) NewHost(ctx context.Context, host *fleet.Host) (*fleet.Host, error) {
	sqlStatement := `
	INSERT INTO hosts (
		osquery_host_id,
		detail_updated_at,
		label_updated_at,
		policy_updated_at,
		node_key,
		hostname,
		uuid,
		platform,
		osquery_version,
		os_version,
		uptime,
		memory,
		seen_time,
		team_id
	)
	VALUES( ?,?,?,?,?,?,?,?,?,?,?,?,?,? )
	`
	result, err := d.writer.ExecContext(
		ctx,
		sqlStatement,
		host.OsqueryHostID,
		host.DetailUpdatedAt,
		host.LabelUpdatedAt,
		host.PolicyUpdatedAt,
		host.NodeKey,
		host.Hostname,
		host.UUID,
		host.Platform,
		host.OsqueryVersion,
		host.OSVersion,
		host.Uptime,
		host.Memory,
		host.SeenTime,
		host.TeamID,
	)
	if err != nil {
		return nil, errors.Wrap(err, "new host")
	}
	id, _ := result.LastInsertId()
	host.ID = uint(id)
	return host, nil
}

func (d *Datastore) SaveHost(ctx context.Context, host *fleet.Host) error {
	return d.withRetryTxx(ctx, func(tx sqlx.ExtContext) error {
		sqlStatement := `
		UPDATE hosts SET
			detail_updated_at = ?,
			label_updated_at = ?,
			policy_updated_at = ?,
			node_key = ?,
			hostname = ?,
			uuid = ?,
			platform = ?,
			osquery_version = ?,
			os_version = ?,
			uptime = ?,
			memory = ?,
			cpu_type = ?,
			cpu_subtype = ?,
			cpu_brand = ?,
			cpu_physical_cores = ?,
			hardware_vendor = ?,
			hardware_model = ?,
			hardware_version = ?,
			hardware_serial = ?,
			computer_name = ?,
			build = ?,
			platform_like = ?,
			code_name = ?,
			cpu_logical_cores = ?,
			seen_time = ?,
			distributed_interval = ?,
			config_tls_refresh = ?,
			logger_tls_period = ?,
			team_id = ?,
			primary_ip = ?,
			primary_mac = ?,
			refetch_requested = ?,
			gigs_disk_space_available = ?,
			percent_disk_space_available = ?
		WHERE id = ?
	`
		_, err := tx.ExecContext(ctx, sqlStatement,
			host.DetailUpdatedAt,
			host.LabelUpdatedAt,
			host.PolicyUpdatedAt,
			host.NodeKey,
			host.Hostname,
			host.UUID,
			host.Platform,
			host.OsqueryVersion,
			host.OSVersion,
			host.Uptime,
			host.Memory,
			host.CPUType,
			host.CPUSubtype,
			host.CPUBrand,
			host.CPUPhysicalCores,
			host.HardwareVendor,
			host.HardwareModel,
			host.HardwareVersion,
			host.HardwareSerial,
			host.ComputerName,
			host.Build,
			host.PlatformLike,
			host.CodeName,
			host.CPULogicalCores,
			host.SeenTime,
			host.DistributedInterval,
			host.ConfigTLSRefresh,
			host.LoggerTLSPeriod,
			host.TeamID,
			host.PrimaryIP,
			host.PrimaryMac,
			host.RefetchRequested,
			host.GigsDiskSpaceAvailable,
			host.PercentDiskSpaceAvailable,
			host.ID,
		)
		if err != nil {
			return errors.Wrapf(err, "save host with id %d", host.ID)
		}

		// Save host pack stats only if it is non-nil. Empty stats should be
		// represented by an empty slice.
		if host.PackStats != nil {
			if err := saveHostPackStatsDB(ctx, tx, host); err != nil {
				return err
			}
		}

		ac, err := d.AppConfig(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to get app config to see if we need to update host users and inventory")
		}

		softwareInventoryEnabled := os.Getenv("FLEET_BETA_SOFTWARE_INVENTORY") != "" || ac.HostSettings.EnableSoftwareInventory
		if host.HostSoftware.Modified && softwareInventoryEnabled && len(host.HostSoftware.Software) > 0 {
			if err := saveHostSoftwareDB(ctx, tx, host); err != nil {
				return errors.Wrap(err, "failed to save host software")
			}
		}

		if host.Modified {
			if host.Additional != nil {
				if err := saveHostAdditionalDB(ctx, tx, host); err != nil {
					return errors.Wrap(err, "failed to save host additional")
				}
			}

			if ac.HostSettings.EnableHostUsers && len(host.Users) > 0 {
				if err := saveHostUsersDB(ctx, tx, host); err != nil {
					return errors.Wrap(err, "failed to save host users")
				}
			}
		}

		host.Modified = false
		return nil
	})
}

func saveHostPackStatsDB(ctx context.Context, db sqlx.ExecerContext, host *fleet.Host) error {
	// Bulk insert software entries
	var args []interface{}
	queryCount := 0
	for _, pack := range host.PackStats {
		for _, query := range pack.QueryStats {
			queryCount++

			args = append(args,
				query.PackName,
				query.ScheduledQueryName,
				host.ID,
				query.AverageMemory,
				query.Denylisted,
				query.Executions,
				query.Interval,
				query.LastExecuted,
				query.OutputSize,
				query.SystemTime,
				query.UserTime,
				query.WallTime,
			)
		}
	}

	if queryCount == 0 {
		return nil
	}

	values := strings.TrimSuffix(strings.Repeat("((SELECT sq.id FROM scheduled_queries sq JOIN packs p ON (sq.pack_id = p.id) WHERE p.name = ? AND sq.name = ?),?,?,?,?,?,?,?,?,?,?),", queryCount), ",")
	sql := fmt.Sprintf(`
			INSERT IGNORE INTO scheduled_query_stats (
				scheduled_query_id,
				host_id,
				average_memory,
				denylisted,
				executions,
				schedule_interval,
				last_executed,
				output_size,
				system_time,
				user_time,
				wall_time
			)
			VALUES %s ON DUPLICATE KEY UPDATE
				scheduled_query_id = VALUES(scheduled_query_id),
				host_id = VALUES(host_id),
				average_memory = VALUES(average_memory),
				denylisted = VALUES(denylisted),
				executions = VALUES(executions),
				schedule_interval = VALUES(schedule_interval),
				last_executed = VALUES(last_executed),
				output_size = VALUES(output_size),
				system_time = VALUES(system_time),
				user_time = VALUES(user_time),
				wall_time = VALUES(wall_time)
		`, values)
	if _, err := db.ExecContext(ctx, sql, args...); err != nil {
		return errors.Wrap(err, "insert pack stats")
	}
	return nil
}

func loadHostPackStatsDB(ctx context.Context, db sqlx.QueryerContext, host *fleet.Host) error {
	sql := `
SELECT
	sqs.scheduled_query_id,
	sqs.average_memory,
	sqs.denylisted,
	sqs.executions,
	sqs.schedule_interval,
	sqs.last_executed,
	sqs.output_size,
	sqs.system_time,
	sqs.user_time,
	sqs.wall_time,
	sq.name AS scheduled_query_name,
	sq.id AS scheduled_query_id,
	sq.query_name AS query_name,
	p.name AS pack_name,
	p.id as pack_id,
	q.description
FROM scheduled_query_stats sqs
	JOIN scheduled_queries sq ON (sqs.scheduled_query_id = sq.id)
	JOIN packs p ON (sq.pack_id = p.id)
	JOIN queries q ON (sq.query_name = q.name)
WHERE host_id = ? AND p.pack_type IS NULL
`
	var stats []fleet.ScheduledQueryStats
	if err := sqlx.SelectContext(ctx, db, &stats, sql, host.ID); err != nil {
		return errors.Wrap(err, "load pack stats")
	}

	packs := map[uint]fleet.PackStats{}
	for _, query := range stats {
		pack := packs[query.PackID]
		pack.PackName = query.PackName
		pack.PackID = query.PackID
		pack.QueryStats = append(pack.QueryStats, query)
		packs[pack.PackID] = pack
	}

	for _, pack := range packs {
		host.PackStats = append(host.PackStats, pack)
	}

	return nil
}

func loadHostUsersDB(ctx context.Context, db sqlx.QueryerContext, host *fleet.Host) error {
	sql := `SELECT username, groupname, uid, user_type FROM host_users WHERE host_id = ? and removed_at IS NULL`
	if err := sqlx.SelectContext(ctx, db, &host.Users, sql, host.ID); err != nil {
		return errors.Wrap(err, "load pack stats")
	}
	return nil
}

func (d *Datastore) DeleteHost(ctx context.Context, hid uint) error {
	err := d.deleteEntity(ctx, hostsTable, hid)
	if err != nil {
		return errors.Wrapf(err, "deleting host with id %d", hid)
	}
	return nil
}

func (d *Datastore) Host(ctx context.Context, id uint) (*fleet.Host, error) {
	sqlStatement := `
		SELECT 
		       h.*, 
		       t.name AS team_name, 
		       (SELECT additional FROM host_additional WHERE host_id = h.id) AS additional,
		       coalesce(failing_policies.count, 0) as failing_policies_count,
		       coalesce(failing_policies.count, 0) as total_issues_count
		FROM hosts h
			LEFT JOIN teams t ON (h.team_id = t.id)
			LEFT JOIN (
		    	SELECT host_id, count(*) as count FROM policy_membership WHERE passes=0
		    	GROUP BY host_id
			) as failing_policies ON (h.id=failing_policies.host_id)
		WHERE h.id = ?
		LIMIT 1
	`
	host := &fleet.Host{}
	err := sqlx.GetContext(ctx, d.reader, host, sqlStatement, id)
	if err != nil {
		return nil, errors.Wrap(err, "get host by id")
	}
	if err := loadHostPackStatsDB(ctx, d.reader, host); err != nil {
		return nil, err
	}
	if err := loadHostUsersDB(ctx, d.reader, host); err != nil {
		return nil, err
	}

	return host, nil
}

func amountEnrolledHostsDB(db sqlx.Queryer) (int, error) {
	var amount int
	err := sqlx.Get(db, &amount, `SELECT count(*) FROM hosts`)
	if err != nil {
		return 0, err
	}
	return amount, nil
}

func (d *Datastore) ListHosts(ctx context.Context, filter fleet.TeamFilter, opt fleet.HostListOptions) ([]*fleet.Host, error) {
	sql := `SELECT
		h.*,
		t.name AS team_name,
		coalesce(failing_policies.count, 0) as failing_policies_count,
		coalesce(failing_policies.count, 0) as total_issues_count
		`

	var params []interface{}

	// Only include "additional" if filter provided.
	if len(opt.AdditionalFilters) == 1 && opt.AdditionalFilters[0] == "*" {
		// All info requested.
		sql += `
		, (SELECT additional FROM host_additional WHERE host_id = h.id) AS additional
		`
	} else if len(opt.AdditionalFilters) > 0 {
		// Filter specific columns.
		sql += `, (SELECT JSON_OBJECT(
			`
		for _, field := range opt.AdditionalFilters {
			sql += `?, JSON_EXTRACT(additional, ?), `
			params = append(params, field, fmt.Sprintf(`$."%s"`, field))
		}
		sql = sql[:len(sql)-2]
		sql += `
		    ) FROM host_additional WHERE host_id = h.id) AS additional
		    `
	}

	sql, params = d.applyHostFilters(opt, sql, filter, params)

	hosts := []*fleet.Host{}
	if err := sqlx.SelectContext(ctx, d.reader, &hosts, sql, params...); err != nil {
		return nil, errors.Wrap(err, "list hosts")
	}

	return hosts, nil
}

func (d *Datastore) applyHostFilters(opt fleet.HostListOptions, sql string, filter fleet.TeamFilter, params []interface{}) (string, []interface{}) {
	policyMembershipJoin := "JOIN policy_membership pm ON (h.id=pm.host_id)"
	if opt.PolicyIDFilter == nil {
		policyMembershipJoin = ""
	} else if opt.PolicyResponseFilter == nil {
		policyMembershipJoin = "LEFT " + policyMembershipJoin
	}

	softwareFilter := "TRUE"
	if opt.SoftwareIDFilter != nil {
		softwareFilter = "EXISTS (SELECT 1 FROM host_software hs WHERE hs.host_id=h.id AND hs.software_id=?)"
		params = append(params, opt.SoftwareIDFilter)
	}

	sql += fmt.Sprintf(`FROM hosts h LEFT JOIN teams t ON (h.team_id = t.id)
		LEFT JOIN (
		    SELECT host_id, count(*) as count FROM policy_membership WHERE passes=0
		    GROUP BY host_id
		) as failing_policies ON (h.id=failing_policies.host_id)
		%s
		WHERE TRUE AND %s AND %s
    `, policyMembershipJoin, d.whereFilterHostsByTeams(filter, "h"), softwareFilter,
	)

	sql, params = filterHostsByStatus(sql, opt, params)
	sql, params = filterHostsByTeam(sql, opt, params)
	sql, params = filterHostsByPolicy(sql, opt, params)
	sql, params = searchLike(sql, params, opt.MatchQuery, hostSearchColumns...)

	sql = appendListOptionsToSQL(sql, opt.ListOptions)
	return sql, params
}

func filterHostsByTeam(sql string, opt fleet.HostListOptions, params []interface{}) (string, []interface{}) {
	if opt.TeamFilter != nil {
		sql += ` AND h.team_id = ?`
		params = append(params, *opt.TeamFilter)
	}
	return sql, params
}

func filterHostsByPolicy(sql string, opt fleet.HostListOptions, params []interface{}) (string, []interface{}) {
	if opt.PolicyIDFilter != nil && opt.PolicyResponseFilter != nil {
		sql += ` AND pm.policy_id = ? AND pm.passes = ?`
		params = append(params, *opt.PolicyIDFilter, *opt.PolicyResponseFilter)
	} else if opt.PolicyIDFilter != nil && opt.PolicyResponseFilter == nil {
		sql += ` AND (pm.policy_id = ? OR pm.policy_id IS NULL) AND pm.passes IS NULL`
		params = append(params, *opt.PolicyIDFilter)
	}
	return sql, params
}

func filterHostsByStatus(sql string, opt fleet.HostListOptions, params []interface{}) (string, []interface{}) {
	switch opt.StatusFilter {
	case "new":
		sql += "AND DATE_ADD(h.created_at, INTERVAL 1 DAY) >= ?"
		params = append(params, time.Now())
	case "online":
		sql += fmt.Sprintf("AND DATE_ADD(h.seen_time, INTERVAL LEAST(h.distributed_interval, h.config_tls_refresh) + %d SECOND) > ?", fleet.OnlineIntervalBuffer)
		params = append(params, time.Now())
	case "offline":
		sql += fmt.Sprintf("AND DATE_ADD(h.seen_time, INTERVAL LEAST(h.distributed_interval, h.config_tls_refresh) + %d SECOND) <= ? AND DATE_ADD(h.seen_time, INTERVAL 30 DAY) >= ?", fleet.OnlineIntervalBuffer)
		params = append(params, time.Now(), time.Now())
	case "mia":
		sql += "AND DATE_ADD(h.seen_time, INTERVAL 30 DAY) <= ?"
		params = append(params, time.Now())
	}
	return sql, params
}

func (d *Datastore) CountHosts(ctx context.Context, filter fleet.TeamFilter, opt fleet.HostListOptions) (int, error) {
	sql := `SELECT count(*) `

	// ignore pagination in count
	opt.Page = 0
	opt.PerPage = 0

	var params []interface{}
	sql, params = d.applyHostFilters(opt, sql, filter, params)

	var count int
	if err := sqlx.GetContext(ctx, d.reader, &count, sql, params...); err != nil {
		return 0, errors.Wrap(err, "count hosts")
	}

	return count, nil
}

func (d *Datastore) CleanupIncomingHosts(ctx context.Context, now time.Time) error {
	sqlStatement := `
		DELETE FROM hosts
		WHERE hostname = '' AND osquery_version = ''
		AND created_at < (? - INTERVAL 5 MINUTE)
	`
	if _, err := d.writer.ExecContext(ctx, sqlStatement, now); err != nil {
		return errors.Wrap(err, "cleanup incoming hosts")
	}

	return nil
}

func (d *Datastore) GenerateHostStatusStatistics(ctx context.Context, filter fleet.TeamFilter, now time.Time) (online, offline, mia, new uint, e error) {
	// The logic in this function should remain synchronized with
	// host.Status and CountHostsInTargets

	sqlStatement := fmt.Sprintf(`
			SELECT
				COALESCE(SUM(CASE WHEN DATE_ADD(seen_time, INTERVAL 30 DAY) <= ? THEN 1 ELSE 0 END), 0) mia,
				COALESCE(SUM(CASE WHEN DATE_ADD(seen_time, INTERVAL LEAST(distributed_interval, config_tls_refresh) + %d SECOND) <= ? AND DATE_ADD(seen_time, INTERVAL 30 DAY) >= ? THEN 1 ELSE 0 END), 0) offline,
				COALESCE(SUM(CASE WHEN DATE_ADD(seen_time, INTERVAL LEAST(distributed_interval, config_tls_refresh) + %d SECOND) > ? THEN 1 ELSE 0 END), 0) online,
				COALESCE(SUM(CASE WHEN DATE_ADD(created_at, INTERVAL 1 DAY) >= ? THEN 1 ELSE 0 END), 0) new
			FROM hosts WHERE %s
			LIMIT 1;
		`, fleet.OnlineIntervalBuffer, fleet.OnlineIntervalBuffer,
		d.whereFilterHostsByTeams(filter, "hosts"),
	)

	counts := struct {
		MIA     uint `db:"mia"`
		Offline uint `db:"offline"`
		Online  uint `db:"online"`
		New     uint `db:"new"`
	}{}
	err := sqlx.GetContext(ctx, d.reader, &counts, sqlStatement, now, now, now, now, now)
	if err != nil && err != sql.ErrNoRows {
		e = errors.Wrap(err, "generating host statistics")
		return
	}

	mia = counts.MIA
	offline = counts.Offline
	online = counts.Online
	new = counts.New
	return online, offline, mia, new, nil
}

// EnrollHost enrolls a host
func (d *Datastore) EnrollHost(ctx context.Context, osqueryHostID, nodeKey string, teamID *uint, cooldown time.Duration) (*fleet.Host, error) {
	if osqueryHostID == "" {
		return nil, fmt.Errorf("missing osquery host identifier")
	}

	var host fleet.Host
	err := d.withRetryTxx(ctx, func(tx sqlx.ExtContext) error {
		zeroTime := time.Unix(0, 0).Add(24 * time.Hour)

		var id int64
		err := sqlx.GetContext(ctx, tx, &host, `SELECT id, last_enrolled_at FROM hosts WHERE osquery_host_id = ?`, osqueryHostID)
		switch {
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return errors.Wrap(err, "check existing")

		case errors.Is(err, sql.ErrNoRows):
			// Create new host record
			sqlInsert := `
				INSERT INTO hosts (
					detail_updated_at,
					label_updated_at,
					policy_updated_at,
					osquery_host_id,
					seen_time,
					node_key,
					team_id
				) VALUES (?, ?, ?, ?, ?, ?, ?)
			`
			result, err := tx.ExecContext(ctx, sqlInsert, zeroTime, zeroTime, zeroTime, osqueryHostID, time.Now().UTC(), nodeKey, teamID)

			if err != nil {
				return errors.Wrap(err, "insert host")
			}

			id, _ = result.LastInsertId()

		default:
			// Prevent hosts from enrolling too often with the same identifier.
			// Prior to adding this we saw many hosts (probably VMs) with the
			// same identifier competing for enrollment and causing perf issues.
			if cooldown > 0 && time.Since(host.LastEnrolledAt) < cooldown {
				return backoff.Permanent(fmt.Errorf("host identified by %s enrolling too often", osqueryHostID))
			}
			id = int64(host.ID)
			// Update existing host record
			sqlUpdate := `
				UPDATE hosts
				SET node_key = ?,
				team_id = ?,
				last_enrolled_at = NOW()
				WHERE osquery_host_id = ?
			`
			_, err := tx.ExecContext(ctx, sqlUpdate, nodeKey, teamID, osqueryHostID)

			if err != nil {
				return errors.Wrap(err, "update host")
			}
		}

		sqlSelect := `
			SELECT * FROM hosts WHERE id = ? LIMIT 1
		`
		err = sqlx.GetContext(ctx, tx, &host, sqlSelect, id)
		if err != nil {
			return errors.Wrap(err, "getting the host to return")
		}

		_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO label_membership (host_id, label_id) VALUES (?, (SELECT id FROM labels WHERE name = 'All Hosts' AND label_type = 1))`, id)
		if err != nil {
			return errors.Wrap(err, "insert new host into all hosts label")
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (d *Datastore) AuthenticateHost(ctx context.Context, nodeKey string) (*fleet.Host, error) {
	// Select everything besides `additional`
	sqlStatement := `SELECT * FROM hosts WHERE node_key = ? LIMIT 1`

	host := &fleet.Host{}
	if err := sqlx.GetContext(ctx, d.reader, host, sqlStatement, nodeKey); err != nil {
		switch err {
		case sql.ErrNoRows:
			return nil, notFound("Host")
		default:
			return nil, errors.New("find host")
		}
	}

	return host, nil
}

func (d *Datastore) MarkHostSeen(ctx context.Context, host *fleet.Host, t time.Time) error {
	sqlStatement := `
		UPDATE hosts SET
			seen_time = ?
		WHERE node_key=?
	`

	_, err := d.writer.ExecContext(ctx, sqlStatement, t, host.NodeKey)
	if err != nil {
		return errors.Wrap(err, "marking host seen")
	}

	host.UpdatedAt = t
	return nil
}

func (d *Datastore) MarkHostsSeen(ctx context.Context, hostIDs []uint, t time.Time) error {
	if len(hostIDs) == 0 {
		return nil
	}

	if err := d.withRetryTxx(ctx, func(tx sqlx.ExtContext) error {
		query := `
		UPDATE hosts SET
			seen_time = ?
		WHERE id IN (?)
	`
		query, args, err := sqlx.In(query, t, hostIDs)
		if err != nil {
			return errors.Wrap(err, "sqlx in")
		}
		query = tx.Rebind(query)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return errors.Wrap(err, "exec update")
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "MarkHostsSeen transaction")
	}

	return nil
}

func (d *Datastore) searchHostsWithOmits(ctx context.Context, filter fleet.TeamFilter, query string, omit ...uint) ([]*fleet.Host, error) {
	hostQuery := transformQuery(query)
	ipQuery := `"` + query + `"`

	sql := fmt.Sprintf(`
			SELECT DISTINCT *
			FROM hosts
			WHERE
			(
				MATCH (hostname, uuid) AGAINST (? IN BOOLEAN MODE)
				OR MATCH (primary_ip, primary_mac) AGAINST (? IN BOOLEAN MODE)
			)
			AND id NOT IN (?) AND %s
			LIMIT 10
		`, d.whereFilterHostsByTeams(filter, "hosts"),
	)

	sql, args, err := sqlx.In(sql, hostQuery, ipQuery, omit)
	if err != nil {
		return nil, errors.Wrap(err, "searching hosts")
	}
	sql = d.reader.Rebind(sql)

	hosts := []*fleet.Host{}

	err = sqlx.SelectContext(ctx, d.reader, &hosts, sql, args...)
	if err != nil {
		return nil, errors.Wrap(err, "searching hosts rebound")
	}

	return hosts, nil
}

func (d *Datastore) searchHostsDefault(ctx context.Context, filter fleet.TeamFilter, omit ...uint) ([]*fleet.Host, error) {
	sql := fmt.Sprintf(`
			SELECT * FROM hosts
			WHERE id NOT in (?) AND %s
			ORDER BY seen_time DESC
			LIMIT 5
		`, d.whereFilterHostsByTeams(filter, "hosts"),
	)

	var in interface{}
	{
		// use -1 if there are no values to omit.
		// Avoids empty args error for `sqlx.In`
		in = omit
		if len(omit) == 0 {
			in = -1
		}
	}

	var hosts []*fleet.Host
	sql, args, err := sqlx.In(sql, in)
	if err != nil {
		return nil, errors.Wrap(err, "searching default hosts")
	}
	sql = d.reader.Rebind(sql)
	err = sqlx.SelectContext(ctx, d.reader, &hosts, sql, args...)
	if err != nil {
		return nil, errors.Wrap(err, "searching default hosts rebound")
	}
	return hosts, nil
}

// SearchHosts find hosts by query containing an IP address, a host name or UUID.
// Optionally pass a list of IDs to omit from the search
func (d *Datastore) SearchHosts(ctx context.Context, filter fleet.TeamFilter, query string, omit ...uint) ([]*fleet.Host, error) {
	hostQuery := transformQuery(query)
	if !queryMinLength(hostQuery) {
		return d.searchHostsDefault(ctx, filter, omit...)
	}
	if len(omit) > 0 {
		return d.searchHostsWithOmits(ctx, filter, query, omit...)
	}

	// Needs quotes to avoid each . marking a word boundary
	ipQuery := `"` + query + `"`

	sql := fmt.Sprintf(`
			SELECT DISTINCT *
			FROM hosts
			WHERE
			(
				MATCH (hostname, uuid) AGAINST (? IN BOOLEAN MODE)
				OR MATCH (primary_ip, primary_mac) AGAINST (? IN BOOLEAN MODE)
			) AND %s
			LIMIT 10
		`, d.whereFilterHostsByTeams(filter, "hosts"),
	)

	hosts := []*fleet.Host{}
	if err := sqlx.SelectContext(ctx, d.reader, &hosts, sql, hostQuery, ipQuery); err != nil {
		return nil, errors.Wrap(err, "searching hosts")
	}

	return hosts, nil

}

func (d *Datastore) HostIDsByName(ctx context.Context, filter fleet.TeamFilter, hostnames []string) ([]uint, error) {
	if len(hostnames) == 0 {
		return []uint{}, nil
	}

	sqlStatement := fmt.Sprintf(`
			SELECT id FROM hosts
			WHERE hostname IN (?) AND %s
		`, d.whereFilterHostsByTeams(filter, "hosts"),
	)

	sql, args, err := sqlx.In(sqlStatement, hostnames)
	if err != nil {
		return nil, errors.Wrap(err, "building query to get host IDs")
	}

	var hostIDs []uint
	if err := sqlx.SelectContext(ctx, d.reader, &hostIDs, sql, args...); err != nil {
		return nil, errors.Wrap(err, "get host IDs")
	}

	return hostIDs, nil

}

func (d *Datastore) HostByIdentifier(ctx context.Context, identifier string) (*fleet.Host, error) {
	sql := `
		SELECT * FROM hosts
		WHERE ? IN (hostname, osquery_host_id, node_key, uuid)
		LIMIT 1
	`
	host := &fleet.Host{}
	err := sqlx.GetContext(ctx, d.reader, host, sql, identifier)
	if err != nil {
		return nil, errors.Wrap(err, "get host by identifier")
	}

	if err := loadHostPackStatsDB(ctx, d.reader, host); err != nil {
		return nil, err
	}

	return host, nil
}

func (d *Datastore) AddHostsToTeam(ctx context.Context, teamID *uint, hostIDs []uint) error {
	if len(hostIDs) == 0 {
		return nil
	}

	return d.withRetryTxx(ctx, func(tx sqlx.ExtContext) error {
		// hosts can only be in one team, so if there's a policy that has a team id and a result from one of our hosts
		// it can only be from the previous team they are being transferred from
		query, args, err := sqlx.In(`DELETE FROM policy_membership_history 
					WHERE policy_id IN (SELECT id FROM policies WHERE team_id IS NOT NULL) AND host_id IN (?)`, hostIDs)
		if err != nil {
			return errors.Wrap(err, "add host to team sqlx in")
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return errors.Wrap(err, "exec AddHostsToTeam delete policy membership history")
		}

		query, args, err = sqlx.In(`UPDATE hosts SET team_id = ? WHERE id IN (?)`, teamID, hostIDs)
		if err != nil {
			return errors.Wrap(err, "sqlx.In AddHostsToTeam")
		}

		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return errors.Wrap(err, "exec AddHostsToTeam")
		}

		return nil
	})
}

func saveHostAdditionalDB(ctx context.Context, exec sqlx.ExecerContext, host *fleet.Host) error {
	sql := `
		INSERT INTO host_additional (host_id, additional)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE additional = VALUES(additional)
	`
	if _, err := exec.ExecContext(ctx, sql, host.ID, host.Additional); err != nil {
		return errors.Wrap(err, "insert additional")
	}

	return nil
}

func saveHostUsersDB(ctx context.Context, tx sqlx.ExtContext, host *fleet.Host) error {
	currentHost := &fleet.Host{ID: host.ID}
	if err := loadHostUsersDB(ctx, tx, currentHost); err != nil {
		return err
	}

	keyForUser := func(u *fleet.HostUser) string { return fmt.Sprintf("%d\x00%s", u.Uid, u.Username) }
	incomingUsers := make(map[string]bool)
	var insertArgs []interface{}
	for _, u := range host.Users {
		insertArgs = append(insertArgs, host.ID, u.Uid, u.Username, u.Type, u.GroupName)
		incomingUsers[keyForUser(&u)] = true
	}

	var removedArgs []interface{}
	for _, u := range currentHost.Users {
		if _, ok := incomingUsers[keyForUser(&u)]; !ok {
			removedArgs = append(removedArgs, u.Username)
		}
	}

	insertValues := strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?),", len(host.Users)), ",")
	insertSql := fmt.Sprintf(
		`INSERT INTO host_users (host_id, uid, username, user_type, groupname) VALUES %s ON DUPLICATE KEY UPDATE removed_at=NULL`,
		insertValues,
	)
	if _, err := tx.ExecContext(ctx, insertSql, insertArgs...); err != nil {
		return errors.Wrap(err, "insert users")
	}

	if len(removedArgs) == 0 {
		return nil
	}
	removedValues := strings.TrimSuffix(strings.Repeat("?,", len(removedArgs)), ",")
	removedSql := fmt.Sprintf(
		`UPDATE host_users SET removed_at = CURRENT_TIMESTAMP WHERE host_id = ? and username IN (%s)`,
		removedValues,
	)
	if _, err := tx.ExecContext(ctx, removedSql, append([]interface{}{host.ID}, removedArgs...)...); err != nil {
		return errors.Wrap(err, "mark users as removed")
	}

	return nil
}

func (d *Datastore) TotalAndUnseenHostsSince(ctx context.Context, daysCount int) (int, int, error) {
	var totalCount, unseenCount int
	err := sqlx.GetContext(ctx, d.reader, &totalCount, "SELECT count(*) FROM hosts")
	if err != nil {
		return 0, 0, errors.Wrap(err, "getting total host count")
	}

	err = sqlx.GetContext(ctx, d.reader, &unseenCount,
		"SELECT count(*) FROM hosts WHERE DATEDIFF(CURRENT_DATE, seen_time) >= ?",
		daysCount,
	)
	if err != nil {
		return 0, 0, errors.Wrap(err, "getting unseen host count")
	}

	return totalCount, unseenCount, nil
}

func (d *Datastore) DeleteHosts(ctx context.Context, ids []uint) error {
	_, err := d.deleteEntities(ctx, hostsTable, ids)
	if err != nil {
		return errors.Wrap(err, "deleting hosts")
	}
	return nil
}

func (d *Datastore) ListPoliciesForHost(ctx context.Context, hid uint) (packs []*fleet.HostPolicy, err error) {
	// instead of using policy_membership, we use the same query but with `where host_id=?` in the subquery
	// if we don't do this, the subquery does a full table scan because the where at the end doesn't affect it
	query := `SELECT 
		p.id, 
		p.query_id, 
		q.name AS query_name, 
		CASE
			WHEN pm.passes = 1 THEN 'pass' 
			WHEN pm.passes = 0 THEN 'fail' 
			ELSE '' 
		END AS response 
	FROM (
	    SELECT * FROM policy_membership_history WHERE id IN (
	        SELECT max(id) AS id FROM policy_membership_history WHERE host_id=? GROUP BY host_id, policy_id
	    )
	) as pm 
	JOIN policies p ON (p.id=pm.policy_id) 
	JOIN queries q ON (p.query_id=q.id)`

	var policies []*fleet.HostPolicy
	if err := sqlx.SelectContext(ctx, d.reader, &policies, query, hid); err != nil {
		return nil, errors.Wrap(err, "get host policies")
	}
	return policies, nil
}
