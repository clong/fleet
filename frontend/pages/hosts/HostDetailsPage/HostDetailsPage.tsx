import React, { useContext, useState } from "react";
import { useDispatch } from "react-redux";
import { Link } from "react-router";
import { Params } from "react-router/lib/Router";
import { useQuery } from "react-query";
import classnames from "classnames";
import { isEmpty, pick, reduce } from "lodash";

import PATHS from "router/paths";
import hostAPI from "services/entities/hosts";
import queryAPI from "services/entities/queries";
import teamAPI from "services/entities/teams";
import { AppContext } from "context/app";
import { IHost } from "interfaces/host";
import { ISoftware } from "interfaces/software";
import { ILabel } from "interfaces/label";
import { ITeam } from "interfaces/team";
import { IQuery } from "interfaces/query";
import { ITableSearchData } from "components/TableContainer/TableContainer"; // @ts-ignore
import { renderFlash } from "redux/nodes/notifications/actions"; // @ts-ignore
import simpleSearch from "utilities/simple_search";

import ReactTooltip from "react-tooltip";
import Spinner from "components/loaders/Spinner";
import Button from "components/buttons/Button";
import Modal from "components/modals/Modal"; // @ts-ignore
import SoftwareVulnerabilities from "pages/hosts/HostDetailsPage/SoftwareVulnerabilities"; // @ts-ignore
import HostUsersListRow from "pages/hosts/HostDetailsPage/HostUsersListRow";
import TableContainer from "components/TableContainer";

import {
  Accordion,
  AccordionItem,
  AccordionItemHeading,
  AccordionItemButton,
  AccordionItemPanel,
} from "react-accessible-accordion";
import {
  humanTimeAgo,
  humanHostUptime,
  humanHostLastSeen,
  humanHostEnrolled,
  humanHostMemory,
  humanHostDetailUpdated,
  secondsToHms,
} from "fleet/helpers"; // @ts-ignore
import SelectQueryModal from "./SelectQueryModal";
import TransferHostModal from "./TransferHostModal";
import {
  generateTableHeaders,
  generateDataSet,
} from "./SoftwareTable/SoftwareTableConfig";
import {
  generatePackTableHeaders,
  generatePackDataSet,
} from "./PackTable/PackTableConfig";
import EmptySoftware from "./EmptySoftware";

import BackChevron from "../../../../assets/images/icon-chevron-down-9x6@2x.png";
import DeleteIcon from "../../../../assets/images/icon-action-delete-14x14@2x.png";
import TransferIcon from "../../../../assets/images/icon-action-transfer-16x16@2x.png";
import QueryIcon from "../../../../assets/images/icon-action-query-16x16@2x.png";

const baseClass = "host-details";

interface IHostDetailsProps {
  router: any;
  params: Params;
}

interface IFleetQueriesResponse {
  queries: IQuery[];
}

interface ITeamsResponse {
  teams: ITeam[];
}

interface IHostResponse {
  host: IHost;
}

const HostDetailsPage = ({
  router,
  params: { host_id },
}: IHostDetailsProps): JSX.Element => {
  const hostIdFromURL = parseInt(host_id, 10);
  const dispatch = useDispatch();
  const {
    isGlobalAdmin,
    isPremiumTier,
    isOnlyObserver,
    isGlobalMaintainer,
  } = useContext(AppContext);
  const canTransferTeam =
    isPremiumTier && (isGlobalAdmin || isGlobalMaintainer);

  const [showDeleteHostModal, setShowDeleteHostModal] = useState<boolean>(
    false
  );
  const [showTransferHostModal, setShowTransferHostModal] = useState<boolean>(
    false
  );
  const [showQueryHostModal, setShowQueryHostModal] = useState<boolean>(false);
  const [
    showRefetchLoadingSpinner,
    setShowRefetchLoadingSpinner,
  ] = useState<boolean>(false);
  const [softwareState, setSoftwareState] = useState<ISoftware[]>([]);

  const { data: fleetQueries, error: fleetQueriesError } = useQuery<
    IFleetQueriesResponse,
    Error,
    IQuery[]
  >("fleet queries", () => queryAPI.loadAll(), {
    enabled: !!hostIdFromURL,
    select: (data: IFleetQueriesResponse) => data.queries,
  });

  const { data: teams, error: teamsError } = useQuery<
    ITeamsResponse,
    Error,
    ITeam[]
  >("teams", () => teamAPI.loadAll(), {
    enabled: !!hostIdFromURL && canTransferTeam,
    select: (data: ITeamsResponse) => data.teams,
  });

  const {
    isLoading: isLoadingHost,
    data: host,
    refetch: fullyReloadHost,
  } = useQuery<IHostResponse, Error, IHost>(
    "host",
    () => hostAPI.load(hostIdFromURL),
    {
      enabled: !!hostIdFromURL,
      select: (data: IHostResponse) => data.host,
      onSuccess: (returnedHost) => {
        setShowRefetchLoadingSpinner(returnedHost.refetch_requested);
      },
      onError: (error) => {
        console.log(error);
        dispatch(
          renderFlash("error", `Unable to load host. Please try again.`)
        );
      },
    }
  );

  // returns a mixture of props from host
  const normalizeEmptyValues = (hostData: any): { [key: string]: any } => {
    return reduce(
      hostData,
      (result, value, key) => {
        if ((Number.isFinite(value) && value !== 0) || !isEmpty(value)) {
          Object.assign(result, { [key]: value });
        } else {
          Object.assign(result, { [key]: "---" });
        }
        return result;
      },
      {}
    );
  };

  const wrapKolideHelper = (
    helperFn: (value: any) => string,
    value: string
  ): any => {
    return value === "---" ? value : helperFn(value);
  };

  const titleData = normalizeEmptyValues(
    pick(host, [
      "status",
      "memory",
      "cpu_type",
      "os_version",
      "enroll_secret_name",
      "detail_updated_at",
      "percent_disk_space_available",
      "gigs_disk_space_available",
    ])
  );

  const aboutData = normalizeEmptyValues(
    pick(host, [
      "seen_time",
      "uptime",
      "last_enrolled_at",
      "hardware_model",
      "hardware_serial",
      "primary_ip",
    ])
  );

  const osqueryData = normalizeEmptyValues(
    pick(host, [
      "config_tls_refresh",
      "logger_tls_period",
      "distributed_interval",
    ])
  );

  const onDestroyHost = async () => {
    if (host) {
      try {
        await hostAPI.destroy(host);
        dispatch(
          renderFlash(
            "success",
            `Host "${host.hostname}" was successfully deleted.`
          )
        );
        router.push(PATHS.MANAGE_HOSTS);
      } catch (error) {
        console.log(error);
        dispatch(
          renderFlash("error", `Host "${host.hostname}" could not be deleted.`)
        );
      } finally {
        setShowDeleteHostModal(false);
      }
    }
  };

  const onRefetchHost = async () => {
    if (host) {
      // Once the user clicks to refetch, the refetch loading spinner should continue spinning
      // unless there is an error or until the user refreshes the page.
      setShowRefetchLoadingSpinner(true);
      try {
        await hostAPI.refetch(host);
      } catch (error) {
        console.log(error);
        dispatch(renderFlash("error", `Host "${host.hostname}" refetch error`));
      }
    }
  };

  const onLabelClick = (label: ILabel) => {
    if (label.name === "All Hosts") {
      return router.push(PATHS.MANAGE_HOSTS);
    }

    return router.push(`${PATHS.MANAGE_HOSTS}/labels/${label.id}`);
  };

  const onTransferHostSubmit = async (team: ITeam) => {
    const teamId = typeof team.id === "number" ? team.id : null;

    try {
      await hostAPI.transferToTeam(teamId, [hostIdFromURL]);

      const successMessage =
        teamId === null
          ? `Host successfully removed from teams.`
          : `Host successfully transferred to  ${team.name}.`;

      dispatch(renderFlash("success", successMessage));
      fullyReloadHost();
      setShowTransferHostModal(false);
    } catch (error) {
      console.log(error);
      dispatch(
        renderFlash("error", "Could not transfer host. Please try again.")
      );
    }
  };

  // Search functionality
  const onTableSearchChange = ({
    searchQuery,
    sortHeader,
    sortDirection,
  }: ITableSearchData) => {
    let sortBy = [];
    if (sortHeader !== "") {
      sortBy = [{ id: sortHeader, direction: sortDirection }];
    }

    if (!searchQuery && host) {
      setSoftwareState(host.software);
      return;
    }

    setSoftwareState(simpleSearch(searchQuery, host?.software));
  };

  const renderDeleteHostModal = () => (
    <Modal
      title="Delete host"
      onExit={() => setShowDeleteHostModal(false)}
      className={`${baseClass}__modal`}
    >
      <>
        <p>
          This action will delete the host <strong>{host?.hostname}</strong>{" "}
          from your Fleet instance.
        </p>
        <p>
          The host will automatically re-enroll when it checks back into Fleet.
        </p>
        <p>
          To prevent re-enrollment, you can uninstall osquery on the host or
          revoke the host&apos;s enroll secret.
        </p>
        <div className={`${baseClass}__modal-buttons`}>
          <Button onClick={onDestroyHost} variant="alert">
            Delete
          </Button>
          <Button
            onClick={() => setShowDeleteHostModal(false)}
            variant="inverse-alert"
          >
            Cancel
          </Button>
        </div>
      </>
    </Modal>
  );

  const renderActionButtons = () => {
    const isOnline = host?.status === "online";

    return (
      <div className={`${baseClass}__action-button-container`}>
        {canTransferTeam && (
          <Button
            onClick={() => setShowTransferHostModal(true)}
            variant="text-icon"
            className={`${baseClass}__transfer-button`}
          >
            <>
              Transfer <img src={TransferIcon} alt="Transfer host icon" />
            </>
          </Button>
        )}
        <div data-tip data-for="query" data-tip-disable={isOnline}>
          <Button
            onClick={() => setShowQueryHostModal(true)}
            variant="text-icon"
            disabled={!isOnline}
            className={`${baseClass}__query-button`}
          >
            <>
              Query <img src={QueryIcon} alt="Query host icon" />
            </>
          </Button>
        </div>
        <ReactTooltip
          place="bottom"
          type="dark"
          effect="solid"
          id="query"
          backgroundColor="#3e4771"
        >
          <span className={`${baseClass}__tooltip-text`}>
            You can’t query <br /> an offline host.
          </span>
        </ReactTooltip>
        {!isOnlyObserver && (
          <Button
            onClick={() => setShowDeleteHostModal(true)}
            variant="text-icon"
          >
            <>
              Delete <img src={DeleteIcon} alt="Delete host icon" />
            </>
          </Button>
        )}
      </div>
    );
  };

  const renderLabels = () => {
    const { labels = [] } = host || {};

    const labelItems = labels.map((label) => {
      return (
        <li className="list__item" key={label.id}>
          <Button
            onClick={() => onLabelClick(label)}
            variant="label"
            className="list__button"
          >
            {label.name}
          </Button>
        </li>
      );
    });

    return (
      <div className="section labels col-50">
        <p className="section__header">Labels</p>
        {labels.length === 0 ? (
          <p className="info-flex__item">
            No labels are associated with this host.
          </p>
        ) : (
          <ul className="list">{labelItems}</ul>
        )}
      </div>
    );
  };

  const renderPacks = () => {
    const pack_stats = host?.pack_stats;
    const wrapperClassName = `${baseClass}__pack-table`;
    const tableHeaders = generatePackTableHeaders();

    let packsAccordion;
    if (pack_stats) {
      packsAccordion = pack_stats.map((pack) => {
        return (
          <AccordionItem key={pack.pack_id}>
            <AccordionItemHeading>
              <AccordionItemButton>{pack.pack_name}</AccordionItemButton>
            </AccordionItemHeading>
            <AccordionItemPanel>
              {pack.query_stats.length === 0 ? (
                <div>There are no schedule queries for this pack.</div>
              ) : (
                <>
                  {!!pack.query_stats.length && (
                    <div className={`${wrapperClassName}`}>
                      <TableContainer
                        columns={tableHeaders}
                        data={generatePackDataSet(pack.query_stats)}
                        isLoading={isLoadingHost}
                        onQueryChange={() => null}
                        resultsTitle={"queries"}
                        defaultSortHeader={"scheduled_query_name"}
                        defaultSortDirection={"asc"}
                        showMarkAllPages={false}
                        isAllPagesSelected={false}
                        emptyComponent={() => <></>}
                        disablePagination
                        disableCount
                      />
                    </div>
                  )}
                </>
              )}
            </AccordionItemPanel>
          </AccordionItem>
        );
      });
    }

    return (
      <div className="section section--packs">
        <p className="section__header">Packs</p>
        {!pack_stats ? (
          <p className="results__data">
            No packs with scheduled queries have this host as a target.
          </p>
        ) : (
          <Accordion allowMultipleExpanded allowZeroExpanded>
            {packsAccordion}
          </Accordion>
        )}
      </div>
    );
  };

  const renderUsers = () => {
    const { users } = host || {};
    const wrapperClassName = `${baseClass}__table`;

    if (users) {
      return (
        <div className="section section--users">
          <p className="section__header">Users</p>
          {users.length === 0 ? (
            <p className="results__data">
              No users were detected on this host.
            </p>
          ) : (
            <div className={`${baseClass}__wrapper`}>
              <table className={wrapperClassName}>
                <thead>
                  <tr>
                    <th>Username</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((hostUser) => {
                    return (
                      <HostUsersListRow
                        key={`host-users-row-${hostUser.username}`}
                        hostUser={hostUser}
                      />
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      );
    }
  };

  const renderSoftware = () => {
    const tableHeaders = generateTableHeaders();

    return (
      <div className="section section--software">
        <p className="section__header">Software</p>

        {host?.software.length === 0 ? (
          <div className="results">
            <p className="results__header">
              No installed software detected on this host.
            </p>
            <p className="results__data">
              Expecting to see software? Try again in a few seconds as the
              system catches up.
            </p>
          </div>
        ) : (
          <>
            <SoftwareVulnerabilities softwareList={host?.software} />
            {host?.software && (
              <TableContainer
                columns={tableHeaders}
                data={generateDataSet(softwareState)}
                isLoading={isLoadingHost}
                defaultSortHeader={"name"}
                defaultSortDirection={"asc"}
                inputPlaceHolder={"Filter software"}
                onQueryChange={onTableSearchChange}
                resultsTitle={"software items"}
                emptyComponent={EmptySoftware}
                showMarkAllPages={false}
                isAllPagesSelected={false}
                searchable
                wideSearch
                disablePagination
              />
            )}
          </>
        )}
      </div>
    );
  };

  const renderRefetch = () => {
    const isOnline = host?.status === "online";

    return (
      <>
        <div
          className="refetch"
          data-tip
          data-for="refetch-tooltip"
          data-tip-disable={isOnline || showRefetchLoadingSpinner}
        >
          <Button
            className={`
              button
              button--unstyled
              ${!isOnline ? "refetch-offline" : ""} 
              ${showRefetchLoadingSpinner ? "refetch-spinner" : "refetch-btn"}
            `}
            disabled={!isOnline}
            onClick={onRefetchHost}
          >
            {showRefetchLoadingSpinner
              ? "Fetching, try refreshing this page in just a moment."
              : "Refetch"}
          </Button>
        </div>
        <ReactTooltip
          place="bottom"
          type="dark"
          effect="solid"
          id="refetch-tooltip"
          backgroundColor="#3e4771"
        >
          <span className={`${baseClass}__tooltip-text`}>
            You can’t fetch data from <br /> an offline host.
          </span>
        </ReactTooltip>
      </>
    );
  };

  const renderHostTeam = () => (
    <div className="info-flex__item info-flex__item--title">
      <span className="info-flex__header">Team</span>
      <span className={`info-flex__data`}>
        {host?.team_name ? (
          `${host?.team_name}`
        ) : (
          <span className="info-flex__no-team">No team</span>
        )}
      </span>
    </div>
  );

  const renderDeviceUser = () => {
    if (host?.device_users && host?.device_users.length > 0) {
      return (
        // max width is added here because this is the only div that needs it
        <div
          className="info-flex__item info-flex__item--title"
          style={{ maxWidth: 216 }}
        >
          <span className="info-flex__header">Device user</span>
          <span className="info-flex__data">{host.device_users[0].email}</span>
        </div>
      );
    }
  };

  const renderDiskSpace = () => {
    if (
      host &&
      (host.gigs_disk_space_available > 0 ||
        host.percent_disk_space_available > 0)
    ) {
      return (
        <span className="info-flex__data">
          <div className="info-flex__disk-space">
            <div
              className={
                titleData.percent_disk_space_available > 20
                  ? "info-flex__disk-space-used"
                  : "info-flex__disk-space-warning"
              }
              style={{
                width: `${100 - titleData.percent_disk_space_available}%`,
              }}
            />
          </div>
          {titleData.gigs_disk_space_available} GB available
        </span>
      );
    }
    return <span className="info-flex__data">No data available</span>;
  };

  const renderMunkiData = () => {
    if (host?.munki) {
      return (
        <>
          <div className="info-grid__block">
            <span className="info-grid__header">Munki last run</span>
            <span className="info-grid__data">
              {humanTimeAgo(host.munki.last_run_time)} days ago
            </span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Munki packages installed</span>
            <span className="info-grid__data">
              {host.munki.packages_intalled_count}
            </span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Munki errors</span>
            <span className="info-grid__data">{host.munki.errors_count}</span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Munki version</span>
            <span className="info-grid__data">{host.munki.version}</span>
          </div>
        </>
      );
    }
  };

  const renderMDMData = () => {
    if (host?.mdm) {
      return (
        <>
          <div className="info-grid__block">
            <span className="info-grid__header">MDM health</span>
            <span className="info-grid__data">{host.mdm?.health}</span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">MDM enrollment URL</span>
            <span className="info-grid__data">{host.mdm.enrollment_url}</span>
          </div>
        </>
      );
    }
  };

  if (isLoadingHost) {
    return <Spinner />;
  }

  const statusClassName = classnames("status", `status--${host?.status}`);
  return (
    <div className={`${baseClass} body-wrap`}>
      <div>
        <Link to={PATHS.MANAGE_HOSTS} className={`${baseClass}__back-link`}>
          <img src={BackChevron} alt="back chevron" id="back-chevron" />
          <span>Back to all hosts</span>
        </Link>
      </div>
      <div className="header title">
        <div className="title__inner">
          <div className="hostname-container">
            <h1 className="hostname">{host?.hostname || "---"}</h1>
            <p className="last-fetched">
              {`Last fetched ${humanHostDetailUpdated(
                titleData.detail_updated_at
              )}`}
              &nbsp;
            </p>
            {renderRefetch()}
          </div>
        </div>
        {renderActionButtons()}
      </div>
      <div className="section title">
        <div className="title__inner">
          <div className="info-flex">
            <div className="info-flex__item info-flex__item--title">
              <span className="info-flex__header">Status</span>
              <span className={`${statusClassName} info-flex__data`}>
                {titleData.status}
              </span>
            </div>
            {isPremiumTier && renderHostTeam()}
            {renderDeviceUser()}
            <div className="info-flex__item info-flex__item--title">
              <span className="info-flex__header">Disk Space</span>
              {renderDiskSpace()}
            </div>
            <div className="info-flex__item info-flex__item--title">
              <span className="info-flex__header">RAM</span>
              <span className="info-flex__data">
                {wrapKolideHelper(humanHostMemory, titleData.memory)}
              </span>
            </div>
            <div className="info-flex__item info-flex__item--title">
              <span className="info-flex__header">CPU</span>
              <span className="info-flex__data">{titleData.cpu_type}</span>
            </div>
            <div className="info-flex__item info-flex__item--title">
              <span className="info-flex__header">OS</span>
              <span className="info-flex__data">{titleData.os_version}</span>
            </div>
          </div>
        </div>
      </div>
      <div className="section about">
        <p className="section__header">About this host</p>
        <div className="info-grid">
          <div className="info-grid__block">
            <span className="info-grid__header">Created at</span>
            <span className="info-grid__data">
              {wrapKolideHelper(humanHostEnrolled, aboutData.last_enrolled_at)}
            </span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Updated at</span>
            <span className="info-grid__data">
              {wrapKolideHelper(humanHostLastSeen, titleData.detail_updated_at)}
            </span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Uptime</span>
            <span className="info-grid__data">
              {wrapKolideHelper(humanHostUptime, aboutData.uptime)}
            </span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Hardware model</span>
            <span className="info-grid__data">{aboutData.hardware_model}</span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Serial number</span>
            <span className="info-grid__data">{aboutData.hardware_serial}</span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">IPv4</span>
            <span className="info-grid__data">{aboutData.primary_ip}</span>
          </div>
          {renderMunkiData()}
          {renderMDMData()}
        </div>
      </div>
      <div className="section osquery col-50">
        <p className="section__header">Agent options</p>
        <div className="info-grid">
          <div className="info-grid__block">
            <span className="info-grid__header">Config TLS refresh</span>
            <span className="info-grid__data">
              {wrapKolideHelper(secondsToHms, osqueryData.config_tls_refresh)}
            </span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Logger TLS period</span>
            <span className="info-grid__data">
              {wrapKolideHelper(secondsToHms, osqueryData.logger_tls_period)}
            </span>
          </div>
          <div className="info-grid__block">
            <span className="info-grid__header">Distributed interval</span>
            <span className="info-grid__data">
              {wrapKolideHelper(secondsToHms, osqueryData.distributed_interval)}
            </span>
          </div>
        </div>
      </div>
      {renderLabels()}
      {renderPacks()}
      {renderUsers()}
      {host?.software && renderSoftware()}
      {showDeleteHostModal && renderDeleteHostModal()}
      {showQueryHostModal && (
        <SelectQueryModal
          host={host}
          onCancel={() => setShowQueryHostModal(false)}
          queries={fleetQueries}
          dispatch={dispatch}
          queryErrors={fleetQueriesError}
          isOnlyObserver={isOnlyObserver}
        />
      )}
      {!!host && showTransferHostModal && (
        <TransferHostModal
          onCancel={() => setShowTransferHostModal(false)}
          onSubmit={onTransferHostSubmit}
          teams={teams || []}
          isGlobalAdmin={isGlobalAdmin as boolean}
        />
      )}
    </div>
  );
};

export default HostDetailsPage;
