/* eslint-disable default-case */
'use strict';

/**
 * MediaMTX WebRTC Client Library
 * Version: 2.0
 */

// ========== Utility Functions ==========

const unquoteCredential = (v) => JSON.parse(`"${v}"`);

const linkToIceServers = (links) =>
  links !== null
    ? links.split(", ").map((link) => {
        const m = link.match(/^<(.+?)>; rel="ice-server"(; username="(.*?)"; credential="(.*?)"; credential-type="password")?/i);
        const ret = {
          urls: [m[1]],
        };

        if (m[3] !== undefined) {
          ret.username = unquoteCredential(m[3]);
          ret.credential = unquoteCredential(m[4]);
          ret.credentialType = "password";
        }

        return ret;
      })
    : [];

const parseOffer = (offer) => {
  const ret = {
    iceUfrag: "",
    icePwd: "",
    medias: [],
  };

  for (const line of offer.split("\r\n")) {
    if (line.startsWith("m=")) {
      ret.medias.push(line.slice("m=".length));
    } else if (ret.iceUfrag === "" && line.startsWith("a=ice-ufrag:")) {
      ret.iceUfrag = line.slice("a=ice-ufrag:".length);
    } else if (ret.icePwd === "" && line.startsWith("a=ice-pwd:")) {
      ret.icePwd = line.slice("a=ice-pwd:".length);
    }
  }

  return ret;
};

const generateSdpFragment = (offerData, candidates) => {
  const candidatesByMedia = {};
  for (const candidate of candidates) {
    const mid = candidate.sdpMLineIndex;
    if (candidatesByMedia[mid] === undefined) {
      candidatesByMedia[mid] = [];
    }
    candidatesByMedia[mid].push(candidate);
  }

  let frag = `a=ice-ufrag:${offerData.iceUfrag}\r\n` + `a=ice-pwd:${offerData.icePwd}\r\n`;

  let mid = 0;

  for (const media of offerData.medias) {
    if (candidatesByMedia[mid] !== undefined) {
      frag += `m=${media}\r\n` + `a=mid:${mid}\r\n`;

      for (const candidate of candidatesByMedia[mid]) {
        frag += `a=${candidate.candidate}\r\n`;
      }
    }
    mid++;
  }

  return frag;
};

const enableStereoOpus = (section) => {
  let opusPayloadFormat = "";
  let lines = section.split("\r\n");

  for (let i = 0; i < lines.length; i++) {
    if (lines[i].startsWith("a=rtpmap:") && lines[i].toLowerCase().includes("opus/")) {
      opusPayloadFormat = lines[i].slice("a=rtpmap:".length).split(" ")[0];
      break;
    }
  }

  if (opusPayloadFormat === "") {
    return section;
  }

  for (let i = 0; i < lines.length; i++) {
    if (lines[i].startsWith(`a=fmtp:${opusPayloadFormat} `)) {
      if (!lines[i].includes("stereo")) {
        lines[i] += ";stereo=1";
      }
      if (!lines[i].includes("sprop-stereo")) {
        lines[i] += ";sprop-stereo=1";
      }
    }
  }

  return lines.join("\r\n");
};

const editOffer = (offer) => {
  const sections = offer.sdp.split("m=");

  for (let i = 0; i < sections.length; i++) {
    const section = sections[i];
    if (section.startsWith("audio")) {
      sections[i] = enableStereoOpus(section);
    }
  }

  offer.sdp = sections.join("m=");
};

// ========== WHEPClient (WebRTC Reader) ==========

class WHEPClient {
  constructor(serverAddress, pathName, videoElement, autoRestart, restartPause, token, progress) {
    this.retryPause = restartPause || 3000;
    this.serverAddress = serverAddress;
    this.pathName = pathName;
    this.videoElement = videoElement;
    this.autoRestart = autoRestart;
    this.token = token;
    this.progress = progress || (() => {});

    this.state = 'running';
    this.pc = null;
    this.restartTimeout = null;
    this.offerData = null;
    this.sessionUrl = null;
    this.queuedCandidates = [];

    this.start();
  }

  start() {
    this.progress({ loading: true });

    const url = `${this.serverAddress}/${this.pathName}/whep`;

    fetch(url, {
      method: "OPTIONS",
      headers: this.#authHeader(),
    })
      .then((res) => this.#onIceServers(res))
      .catch((err) => {
        console.log("error: " + err);
        this.progress({ error: err });
        this.#scheduleRestart();
      });
  }

  #authHeader() {
    if (this.token) {
      return { Authorization: `Bearer ${this.token}` };
    }
    return {};
  }

  #onIceServers(res) {
    if (this.state !== 'running') {
      return;
    }

    this.pc = new RTCPeerConnection({
      iceServers: linkToIceServers(res.headers.get("Link")),
      sdpSemantics: 'unified-plan',
    });

    const direction = "recvonly";
    this.pc.addTransceiver("video", { direction });
    this.pc.addTransceiver("audio", { direction });

    this.pc.onicecandidate = (evt) => this.#onLocalCandidate(evt);
    this.pc.onconnectionstatechange = () => this.#onConnectionState();

    this.pc.ontrack = (evt) => {
      this.videoElement.srcObject = evt.streams[0];
      this.videoElement.play()
        .then(() => {
          this.progress({ loading: false });
        })
        .catch((error) => {
          console.log("autoplay prevented:", error);
        });
    };

    this.pc.createOffer().then((offer) => this.#onLocalOffer(offer));
  }

  #onLocalOffer(offer) {
    if (this.state !== 'running') {
      return;
    }

    editOffer(offer);

    this.offerData = parseOffer(offer.sdp);
    this.pc.setLocalDescription(offer);

    const url = `${this.serverAddress}/${this.pathName}/whep`;

    fetch(url, {
      method: "POST",
      headers: {
        ...this.#authHeader(),
        "Content-Type": "application/sdp",
      },
      body: offer.sdp,
    })
      .then((res) => {
        if (res.status !== 201) {
          this.progress({ error: { code: res.status } });
          throw new Error(`bad status code: ${res.status}`);
        }
        this.sessionUrl = new URL(res.headers.get("location"), url).toString();
        return res.text();
      })
      .then((sdp) =>
        this.#onRemoteAnswer(
          new RTCSessionDescription({
            type: "answer",
            sdp,
          })
        )
      )
      .catch((err) => {
        console.log("error: " + err);
        this.#scheduleRestart();
      });
  }

  #onConnectionState() {
    if (this.state !== 'running') {
      return;
    }

    console.log("peer connection state:", this.pc.connectionState);

    if (this.pc.connectionState === 'failed' || this.pc.connectionState === 'closed') {
      this.#scheduleRestart();
    } else if (this.pc.connectionState === 'connected') {
      this.progress({ loading: false });
    }
  }

  #onRemoteAnswer(answer) {
    if (this.state !== 'running') {
      return;
    }

    this.pc.setRemoteDescription(answer);

    if (this.queuedCandidates.length !== 0) {
      this.#sendLocalCandidates(this.queuedCandidates);
      this.queuedCandidates = [];
    }
  }

  #onLocalCandidate(evt) {
    if (this.state !== 'running') {
      return;
    }

    if (evt.candidate !== null) {
      if (this.sessionUrl === null) {
        this.queuedCandidates.push(evt.candidate);
      } else {
        this.#sendLocalCandidates([evt.candidate]);
      }
    }
  }

  #sendLocalCandidates(candidates) {
    fetch(this.sessionUrl, {
      method: "PATCH",
      headers: {
        ...this.#authHeader(),
        "Content-Type": "application/trickle-ice-sdpfrag",
        "If-Match": "*",
      },
      body: generateSdpFragment(this.offerData, candidates),
    })
      .then((res) => {
        if (res.status !== 204) {
          throw new Error(`bad status code: ${res.status}`);
        }
      })
      .catch((err) => {
        console.log("error sending candidates: " + err);
        this.#scheduleRestart();
      });
  }

  #scheduleRestart() {
    if (this.restartTimeout !== null) {
      return;
    }

    this.state = 'restarting';

    if (this.pc !== null) {
      this.pc.close();
      this.pc = null;
    }

    if (this.sessionUrl) {
      fetch(this.sessionUrl, {
        method: "DELETE",
        headers: this.#authHeader(),
      }).catch((err) => {
        console.log("delete session error: " + err);
      });
    }

    this.sessionUrl = null;
    this.queuedCandidates = [];

    if (this.autoRestart) {
      this.restartTimeout = window.setTimeout(() => {
        this.restartTimeout = null;
        this.state = 'running';
        this.start();
      }, this.retryPause);
    }
  }

  close() {
    this.state = 'closed';
    this.autoRestart = false;

    if (this.restartTimeout !== null) {
      clearTimeout(this.restartTimeout);
      this.restartTimeout = null;
    }

    if (this.pc) {
      this.pc.close();
      this.pc = null;
    }

    if (this.videoElement) {
      this.videoElement.srcObject = null;
      this.videoElement.src = "";
    }

    console.log("WHEPClient closed");
  }
}

// ========== MRLS Main Class ==========

class MRLS {
  static VERSION() {
    return "v2.0";
  }

  static serverAddress = "";
  static options = {};

  static init(url, options) {
    this.serverAddress = url;
    this.options = options || {};
    return this;
  }

  static playStreaming({ pathName, videoElement, autoRestart = false, restartPause, progress }) {
    if (!progress) {
      progress = () => {};
    }
    return new WHEPClient(
      this.serverAddress,
      pathName,
      videoElement,
      autoRestart,
      restartPause,
      this.options.token,
      progress
    );
  }

  static getPathInfo(pathName) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/paths/get/${pathName}`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static getPathsList() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/paths/list`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static getRecordTasks() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/record/tasks`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static getFiles(date) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/record/date/files?date=${date}`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static record(pathName, action, taskOutMinutes, fileName) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/record/${action}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify({
          name: pathName.toString(),
          videoFormat: "mp4",
          taskOutMinutes: taskOutMinutes,
          fileName: fileName,
        }),
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static snapshot(pathName) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/snapshot?name=${pathName}&fileType=url`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static snapshotStreaming(pathName, imageCopy) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      let url = `${this.serverAddress}/api/v2/publish/snapshot?name=${pathName}&fileType=url`;
      if (imageCopy) {
        url = url + `&imageCopy=${JSON.stringify(imageCopy)}`;
      }
      fetch(url, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static dashboard() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/dashboard`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static rename(fullPath, name) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/file/rename`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify({
          fullPath: fullPath,
          name: name,
        }),
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }

  static del(fullPath, name) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/file/del`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify({
          fullPath: fullPath,
          name: name,
        }),
      })
        .then((response) => response.json())
        .then((data) => {
          resolve(data);
        })
        .catch((error) => {
          reject(error);
        });
    });
  }
}

// Export for use
if (typeof module !== 'undefined' && module.exports) {
  module.exports = MRLS;
}
