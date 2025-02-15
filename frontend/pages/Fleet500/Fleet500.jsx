import React, { Component } from "react";
import PropTypes from "prop-types";
import { connect } from "react-redux";
import { noop } from "lodash";
import { resetErrors } from "redux/nodes/errors500/actions";
import errorsInterface from "interfaces/errors";
import { Link } from "react-router";

import PATHS from "router/paths";

import Button from "components/buttons/Button";

import fleetLogoText from "../../../assets/images/fleet-logo-text-white.svg";
import backgroundImg from "../../../assets/images/500.svg";
import githubLogo from "../../../assets/images/github-mark-white-24x24@2x.png";
import slackLogo from "../../../assets/images/logo-slack-24x24@2x.png";

const baseClass = "fleet-500";

class Fleet500 extends Component {
  static propTypes = {
    errors: errorsInterface,
    dispatch: PropTypes.func,
  };

  static defaultProps = {
    dispatch: noop,
  };

  constructor(props) {
    super(props);

    this.state = {
      showErrorMessage: false,
    };
  }

  componentWillUnmount() {
    const { dispatch } = this.props;
    dispatch(resetErrors());
  }

  onShowErrorMessage = () => {
    this.setState({ showErrorMessage: true });
  };

  renderError = () => {
    const { errors } = this.props;
    const errorMessage = errors ? errors.base : null;
    const { showErrorMessage } = this.state;
    const { onShowErrorMessage } = this;

    if (errorMessage && !showErrorMessage) {
      // We only show the button when errorMessage exists
      // and showErrorMessage is set to false
      return (
        <button className="button button--grey" onClick={onShowErrorMessage}>
          Show error
        </button>
      );
    }

    if (errorMessage && showErrorMessage) {
      // We only show the error message when errorMessage exists
      // and showErrorMessage is set to true
      return (
        <div className="error-message-container">
          <p>{errorMessage}</p>
        </div>
      );
    }

    return false;
  };

  render() {
    const { renderError } = this;

    return (
      <div className={baseClass}>
        <header className="primary-header">
          <Link to={PATHS.HOME}>
            <img
              className="primary-header__logo"
              src={fleetLogoText}
              alt="Fleet logo"
            />
          </Link>
        </header>
        <img
          className="background-image"
          src={backgroundImg}
          alt="500 background"
        />
        <main>
          <h1>
            <span>500:</span> Oh, something went wrong.
          </h1>
          <p>Please file an issue if you believe this is a bug.</p>
          {renderError()}
          <div className={`${baseClass}__button-wrapper`}>
            <a
              href="https://osquery.slack.com/join/shared_invite/zt-h29zm0gk-s2DBtGUTW4CFel0f0IjTEw#/"
              target="_blank"
              rel="noopener noreferrer"
            >
              <Button
                type="button"
                variant="unstyled"
                className={`${baseClass}__slack-btn`}
              >
                <img src={slackLogo} alt="Slack icon" />
                Get help on Slack
              </Button>
            </a>
            <a
              href="https://github.com/fleetdm/fleet/issues/new?assignees=&labels=bug%2C%3Areproduce&template=bug-report.md&title="
              target="_blank"
              rel="noopener noreferrer"
            >
              <Button type="button">
                <img src={githubLogo} alt="Github icon" />
                File an issue
              </Button>
            </a>
          </div>
        </main>
      </div>
    );
  }
}

const mapStateToProps = (state) => {
  const { errors } = state.errors500;
  return {
    errors,
  };
};

export default connect(mapStateToProps)(Fleet500);
