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

  /**
   * 播放 WebRTC 流
   * @param {Object} params - 参数对象
   * @param {string} params.pathName - 流路径名称
   * @param {HTMLVideoElement} params.videoElement - 视频元素
   * @param {boolean} params.autoRestart - 是否自动重连 (默认 false)
   * @param {number} params.restartPause - 重连间隔毫秒数
   * @param {Function} params.progress - 进度回调函数
   * @returns {WHEPClient} WHEP 客户端实例
   */
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

  /**
   * 获取服务器信息
   * GET /api/v2/info
   * @returns {Promise<Object>} 返回服务器版本和启动时间
   * @example
   * // Response:
   * {
   *   "version": "v2-pro-1.0.0",
   *   "started": "2024-01-01T00:00:00Z"
   * }
   */
  static getInfo() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/info`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取健康状态
   * GET /api/v2/health
   * @returns {Promise<Object>} 返回健康状态
   * @example
   * // Response:
   * {
   *   "status": "healthy",
   *   "version": "1.0.0",
   *   "uptime": "1h30m"
   * }
   */
  static getHealth() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/health`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取统计信息
   * GET /api/v2/stats
   * @returns {Promise<Object>} 返回系统统计信息
   * @example
   * // Response:
   * {
   *   "version": "1.0.0",
   *   "uptime": "1h30m",
   *   "pathsCount": 5,
   *   "servers": {
   *     "rtsp": true,
   *     "rtmp": true,
   *     "webrtc": true
   *   },
   *   "config": {
   *     "logLevel": "info",
   *     "configuredPaths": 10
   *   }
   * }
   */
  static getStats() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/stats`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取全局配置
   * GET /api/v2/config/global/get
   * @returns {Promise<Object>} 返回全局配置
   */
  static getGlobalConfig() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/config/global/get`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 更新全局配置
   * PATCH /api/v2/config/global/patch
   * @param {Object} config - 配置对象
   * @returns {Promise<void>}
   */
  static patchGlobalConfig(config) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/config/global/patch`, {
        method: "PATCH",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify(config),
      })
        .then((response) => {
          if (response.ok) {
            resolve();
          } else {
            response.json().then(data => reject(data));
          }
        })
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取路径信息
   * GET /api/v2/paths/get/{name}
   * @param {string} pathName - 路径名称
   * @returns {Promise<Object>} 返回路径详细信息
   * @example
   * // Response:
   * {
   *   "name": "stream1",
   *   "confName": "stream1",
   *   "source": "publisher",
   *   "ready": true,
   *   "readyTime": "2024-01-01T00:00:00Z",
   *   "tracks": ["H264", "Opus"],
   *   "bytesReceived": 1024000
   * }
   */
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
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取路径列表
   * GET /api/v2/paths/list
   * @returns {Promise<Object>} 返回所有路径列表
   * @example
   * // Response:
   * {
   *   "itemCount": 2,
   *   "pageCount": 1,
   *   "items": [
   *     {
   *       "name": "stream1",
   *       "confName": "stream1",
   *       "source": "publisher",
   *       "ready": true
   *     }
   *   ]
   * }
   */
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
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 查询路径信息（包含录制状态）
   * GET /api/v2/paths/query
   * @returns {Promise<Object>} 返回路径查询结果
   * @example
   * // Response:
   * {
   *   "success": true,
   *   "result": [
   *     {
   *       "name": "stream1",
   *       "sourceName": "摄像头1",
   *       "sourceReady": true,
   *       "taskEndTime": "2024-01-01T12:00:00Z",
   *       "order": 0,
   *       "groupName": "前端",
   *       "showList": true
   *     }
   *   ]
   * }
   */
  static getPathsQuery() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/paths/query`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取路径信息（扩展版本）
   * GET /api/v2/paths/get2/{name}
   * @param {string} pathName - 路径名称
   * @returns {Promise<Object>} 返回路径扩展信息
   */
  static getPathInfo2(pathName) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/paths/get2/${pathName}`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 发送消息到路径
   * POST /api/v2/paths/message
   * @param {Object} message - 消息对象
   * @param {string} message.pathName - 路径名称
   * @param {string} message.type - 消息类型
   * @param {*} message.data - 消息数据
   * @returns {Promise<Object>}
   */
  static postMessage(message) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/paths/message`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify(message),
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取 RTSP 连接列表
   * GET /api/v2/rtspconns/list
   * @returns {Promise<Object>} 返回 RTSP 连接列表
   */
  static getRTSPConnsList() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/rtspconns/list`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取 RTSP 会话列表
   * GET /api/v2/rtspsessions/list
   * @returns {Promise<Object>} 返回 RTSP 会话列表
   */
  static getRTSPSessionsList() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/rtspsessions/list`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取 RTMP 连接列表
   * GET /api/v2/rtmpconns/list
   * @returns {Promise<Object>} 返回 RTMP 连接列表
   */
  static getRTMPConnsList() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/rtmpconns/list`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取 WebRTC 会话列表
   * GET /api/v2/webrtcsessions/list
   * @returns {Promise<Object>} 返回 WebRTC 会话列表
   */
  static getWebRTCSessionsList() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/webrtcsessions/list`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 开始录制
   * POST /api/v2/record/start
   * @param {string} pathName - 路径名称
   * @param {string} action - 动作 ('start')
   * @param {number} taskOutMinutes - 录制超时时间（分钟）
   * @param {string} fileName - 自定义文件名（可选）
   * @returns {Promise<Object>} 返回录制任务信息
   * @example
   * // Request:
   * {
   *   "name": "stream1",
   *   "videoFormat": "mp4",
   *   "taskOutMinutes": 30,
   *   "fileName": "custom_name"
   * }
   * // Response:
   * {
   *   "success": true,
   *   "existed": false,
   *   "id": "task-uuid",
   *   "name": "stream1",
   *   "fileName": "20240101-1200-abcd1234.mp4",
   *   "filePath": "/20240101/20240101-1200-abcd1234.mp4",
   *   "fullPath": "/path/to/recordings/20240101/20240101-1200-abcd1234.mp4",
   *   "fileURL": "http://server/res/20240101/20240101-1200-abcd1234.mp4",
   *   "taskEndTime": "2024-01-01T12:30:00Z"
   * }
   */
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
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 停止录制
   * POST /api/v2/record/stop
   * @param {string} pathName - 路径名称
   * @returns {Promise<Object>} 返回停止结果
   * @example
   * // Request:
   * {
   *   "name": "stream1"
   * }
   * // Response:
   * {
   *   "success": true,
   *   "name": "stream1",
   *   "fileName": "20240101-1200-abcd1234.mp4",
   *   "filePath": "/20240101/20240101-1200-abcd1234.mp4",
   *   "fullPath": "/path/to/recordings/20240101/20240101-1200-abcd1234.mp4",
   *   "fileURL": "http://server/res/20240101/20240101-1200-abcd1234.mp4"
   * }
   */
  static stopRecord(pathName) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/record/stop`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify({
          name: pathName,
        }),
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取录制任务列表
   * GET /api/v2/record/tasks
   * @returns {Promise<Object>} 返回所有录制任务
   * @example
   * // Response:
   * {
   *   "tasks": [
   *     {
   *       "id": "task-uuid",
   *       "name": "stream1",
   *       "fileName": "20240101-1200-abcd1234.mp4",
   *       "startTime": "2024-01-01T12:00:00Z",
   *       "endTime": "2024-01-01T12:30:00Z"
   *     }
   *   ]
   * }
   */
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
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取指定路径的录制任务
   * GET /api/v2/record/task/{name}
   * @param {string} pathName - 路径名称
   * @returns {Promise<Object>} 返回录制任务信息
   */
  static getRecordTask(pathName) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/record/task/${pathName}`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取指定日期的录制文件列表
   * GET /api/v2/record/date/files?date={date}
   * @param {string} date - 日期 (格式: YYYYMMDD)
   * @returns {Promise<Object>} 返回文件列表
   * @example
   * // Response:
   * {
   *   "files": [
   *     {
   *       "name": "20240101-1200-abcd1234.mp4",
   *       "path": "/20240101/20240101-1200-abcd1234.mp4",
   *       "url": "http://server/res/20240101/20240101-1200-abcd1234.mp4",
   *       "size": 102400,
   *       "modTime": "2024-01-01T12:30:00Z"
   *     }
   *   ]
   * }
   */
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
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取收藏文件列表
   * GET /api/v2/record/favorite/files
   * @returns {Promise<Object>} 返回收藏文件列表
   */
  static getFavoriteFiles() {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/record/favorite/files`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 获取仪表盘信息
   * GET /api/v2/dashboard
   * @returns {Promise<Object>} 返回仪表盘数据
   */
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
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 设备截图（从设备 HTTP API）
   * GET /api/v2/snapshot?name={name}&fileType={fileType}
   * @param {string} pathName - 路径名称
   * @param {string} fileType - 文件类型 ('url'|'file'|'stream')
   * @param {Object} options - 可选参数
   * @param {string} options.fileName - 自定义文件名
   * @param {number} options.brightness - 亮度调整 (-100 to 100)
   * @param {number} options.contrast - 对比度调整 (-100 to 100)
   * @param {number} options.saturation - 饱和度调整 (-100 to 100)
   * @param {number} options.thumbnailSize - 缩略图宽度（从配置读取）
   * @returns {Promise<Object>} 返回截图信息或图片流
   * @example
   * // Response (fileType='url'):
   * {
   *   "success": true,
   *   "filePath": "/20240101/snapshot.jpg",
   *   "fileURL": "http://server/res/20240101/snapshot.jpg",
   *   "filename": "snapshot.jpg",
   *   "fullPath": "/path/to/recordings/20240101/snapshot.jpg",
   *   "original": "snapshot.jpg",
   *   "thumbnail": "thumbnail-snapshot.jpg",
   *   "width": 1920,
   *   "height": 1080
   * }
   */
  static snapshot(pathName, fileType = 'url', options = {}) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      let url = `${this.serverAddress}/api/v2/snapshot?name=${pathName}&fileType=${fileType}`;
      if (options.fileName) url += `&fileName=${options.fileName}`;
      if (options.brightness) url += `&brightness=${options.brightness}`;
      if (options.contrast) url += `&contrast=${options.contrast}`;
      if (options.saturation) url += `&saturation=${options.saturation}`;
      if (options.thumbnailSize) url += `&thumbnailSize=${options.thumbnailSize}`;

      fetch(url, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 流截图（使用 FFmpeg 从流中截取）
   * GET /api/v2/publish/snapshot?name={name}&fileType={fileType}
   * @param {string} pathName - 路径名称
   * @param {string} fileType - 文件类型 ('url'|'file'|'stream')
   * @param {Object} imageCopy - 裁剪参数 {x, y, w, h}
   * @param {Object} options - 可选参数（同 snapshot）
   * @returns {Promise<Object>} 返回截图信息或图片流
   */
  static snapshotStreaming(pathName, fileType = 'url', imageCopy, options = {}) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      let url = `${this.serverAddress}/api/v2/publish/snapshot?name=${pathName}&fileType=${fileType}`;
      if (imageCopy) {
        url = url + `&imageCopy=${JSON.stringify(imageCopy)}`;
      }
      if (options.fileName) url += `&fileName=${options.fileName}`;
      if (options.brightness) url += `&brightness=${options.brightness}`;
      if (options.contrast) url += `&contrast=${options.contrast}`;
      if (options.saturation) url += `&saturation=${options.saturation}`;
      if (options.thumbnailSize) url += `&thumbnailSize=${options.thumbnailSize}`;

      fetch(url, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 原生截图（纯 Go 实现，仅支持 MJPEG 格式）
   * GET /api/v2/snapshot/native?name={name}&fileType={fileType}
   * @param {string} pathName - 路径名称
   * @param {string} fileType - 文件类型 ('url'|'file'|'stream')
   * @param {Object} options - 可选参数（同 snapshot）
   * @returns {Promise<Object>} 返回截图信息或图片流
   * @note 仅支持 MJPEG 格式流，H264/H265 会返回错误
   */
  static snapshotNative(pathName, fileType = 'url', options = {}) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      let url = `${this.serverAddress}/api/v2/snapshot/native?name=${pathName}&fileType=${fileType}`;
      if (options.fileName) url += `&fileName=${options.fileName}`;
      if (options.brightness) url += `&brightness=${options.brightness}`;
      if (options.contrast) url += `&contrast=${options.contrast}`;
      if (options.saturation) url += `&saturation=${options.saturation}`;
      if (options.thumbnailSize) url += `&thumbnailSize=${options.thumbnailSize}`;

      fetch(url, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * MJPEG 实时流（返回 multipart MJPEG 流）
   * GET /api/v2/snapshot/mjpeg?name={name}
   * @param {string} pathName - 路径名称
   * @returns {string} 返回流 URL，可直接用于 <img src="...">
   * @note 仅支持 MJPEG 格式流
   * @example
   * const url = MRLS.snapshotMJPEGUrl('stream1');
   * imgElement.src = url;
   */
  static snapshotMJPEGUrl(pathName) {
    const token = this.options.token;
    let url = `${this.serverAddress}/api/v2/snapshot/mjpeg?name=${pathName}`;
    if (token) {
      url += `&token=${token}`;
    }
    return url;
  }

  /**
   * 获取截图配置
   * GET /api/v2/snapshot/config/{name}
   * @param {string} pathName - 路径名称
   * @returns {Promise<Object>} 返回截图配置
   */
  static getSnapshotConfig(pathName) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/snapshot/config/${pathName}`, {
        method: "GET",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 保存截图配置
   * POST /api/v2/snapshot/config/{name}
   * @param {string} pathName - 路径名称
   * @param {Object} config - 配置对象
   * @returns {Promise<Object>}
   */
  static saveSnapshotConfig(pathName, config) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/snapshot/config/${pathName}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify(config),
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 设备代理接口
   * ANY /api/v2/proxy/device/{path}
   * @param {string} method - HTTP 方法
   * @param {string} path - 设备路径
   * @param {*} data - 请求数据（可选）
   * @returns {Promise<Object>}
   */
  static proxyDevice(method, path, data) {
    const token = this.options.token;
    const options = {
      method: method,
      headers: {
        "Content-Type": "application/json",
        Authorization: token ? `Bearer ${token}` : undefined,
      },
    };
    if (data) {
      options.body = JSON.stringify(data);
    }
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/proxy/device/${path}`, options)
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 导出为 MP4
   * POST /api/v2/file/export/mp4
   * @param {Object} params - 导出参数
   * @param {string} params.fullPath - 源文件完整路径
   * @param {string} params.name - 输出文件名
   * @returns {Promise<Object>} 返回导出结果
   */
  static exportMP4(params) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/file/export/mp4`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify(params),
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 重命名文件
   * POST /api/v2/file/rename
   * @param {string} fullPath - 文件完整路径
   * @param {string} name - 新文件名
   * @returns {Promise<Object>} 返回重命名结果
   * @example
   * // Request:
   * {
   *   "fullPath": "/path/to/recordings/20240101/old.mp4",
   *   "name": "new.mp4"
   * }
   * // Response:
   * {
   *   "success": true
   * }
   */
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
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 删除文件
   * POST /api/v2/file/del
   * @param {string} fullPath - 文件完整路径
   * @param {string} name - 文件名
   * @returns {Promise<Object>} 返回删除结果
   * @example
   * // Request:
   * {
   *   "fullPath": "/path/to/recordings/20240101/file.mp4",
   *   "name": "file.mp4"
   * }
   * // Response:
   * {
   *   "success": true
   * }
   */
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
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }

  /**
   * 移动/收藏文件
   * POST /api/v2/file/favorite
   * @param {string} fullPath - 文件完整路径
   * @param {string} targetDir - 目标目录
   * @returns {Promise<Object>} 返回移动结果
   */
  static favorite(fullPath, targetDir) {
    const token = this.options.token;
    return new Promise((resolve, reject) => {
      fetch(`${this.serverAddress}/api/v2/file/favorite`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: token ? `Bearer ${token}` : undefined,
        },
        body: JSON.stringify({
          fullPath: fullPath,
          targetDir: targetDir,
        }),
      })
        .then((response) => response.json())
        .then((data) => resolve(data))
        .catch((error) => reject(error));
    });
  }
}

// Export for use
if (typeof module !== 'undefined' && module.exports) {
  module.exports = MRLS;
}
