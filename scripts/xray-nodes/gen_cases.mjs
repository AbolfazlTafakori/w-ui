// Every kind of inbound each test panel has, with its transports and
// securities, built by the inbound form's own builder (web/src/lib/
// xrayInbound.js) -- so what is tested is what the form sends.
//
// usage: node gen_cases.mjs CERT_PEM KEY_PEM > cases.json
//   CERT_PEM, KEY_PEM  a certificate pasted into the inbounds of panels that
//                      run in docker (3x-ui 2, Pasarguard's node)
//   TLS_CERT_FILE, TLS_KEY_FILE, TLS_SERVER_NAME (environment)
//                      the certificate files on the 3x-ui 3 host, and its name
import fs from 'node:fs'
import crypto from 'node:crypto'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const { buildInbound, newInboundState } = await import(pathToFileURL(path.join(here, '../../web/src/lib/xrayInbound.js')).href)

const [certFile, keyFile] = process.argv.slice(2)
if (!certFile || !keyFile) {
  console.error('usage: node gen_cases.mjs CERT_PEM KEY_PEM > cases.json')
  process.exit(2)
}
const need = (name) => process.env[name] || (console.error(`${name} is not set`), process.exit(2))
const certText = fs.readFileSync(certFile, 'utf8')
const keyText = fs.readFileSync(keyFile, 'utf8')
const tlsCert = need('TLS_CERT_FILE')
const tlsKey = need('TLS_KEY_FILE')
const tlsName = need('TLS_SERVER_NAME')

// Keys as /api/xray/keys draws them, drawn once per run.
const kp = crypto.generateKeyPairSync('x25519').privateKey.export({ format: 'jwk' })
const REALITY = { privateKey: kp.d, publicKey: kp.x }
const b64 = (n) => crypto.randomBytes(n).toString('base64')

const streams = new Set(['vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria', 'masque', 'mixed', 'tunnel', 'http'])
function make(panel, name, port, protocol, edit) {
  const x = newInboundState()
  x.protocol = protocol
  x.security = 'none'
  x.network = 'tcp'
  x.ss.password = ''
  edit(x)
  return { panel, name, port, protocol, network: x.network, security: x.security,
    xray: buildInbound(x, { stream: streams.has(protocol) }, panel === 'xui-old' ? 'xui' : panel) }
}
const reality = (x) => { x.security = 'reality'; Object.assign(x.reality, REALITY, { shortIds: 'a1b2c3d4', target: 'www.speedtest.net:443', serverNames: 'www.speedtest.net' }) }
const tlsFiles = (x) => { x.security = 'tls'; Object.assign(x.tls, { certMode: 'file', certificateFile: tlsCert, keyFile: tlsKey, serverName: tlsName }) }
const tlsText = (x) => { x.security = 'tls'; Object.assign(x.tls, { certMode: 'text', certificate: certText, key: keyText, serverName: 'test.example' }) }

const out = []
// 3x-ui 3.10: every kind it has, the transports and securities over them.
const v3 = (n, p, proto, e) => out.push(make('xui', n, p, proto, e))
v3('t-vless-reality', 35001, 'vless', reality)
v3('t-vless-ws', 35002, 'vless', (x) => { x.network = 'ws'; x.ws.path = '/v' })
v3('t-vless-grpc-reality', 35003, 'vless', (x) => { x.network = 'grpc'; x.grpc.serviceName = 'gs'; reality(x) })
v3('t-vless-xhttp-reality', 35004, 'vless', (x) => { x.network = 'xhttp'; x.xhttp.path = '/xh'; reality(x) })
v3('t-vless-tls', 35005, 'vless', tlsFiles)
v3('t-vless-hu', 35006, 'vless', (x) => { x.network = 'httpupgrade'; x.hu.path = '/hu' })
v3('t-vless-kcp', 35007, 'vless', (x) => { x.network = 'kcp'; x.kcp.seed = 'seed1' })
v3('t-vmess-tcp', 35008, 'vmess', () => {})
v3('t-vmess-ws-tls', 35009, 'vmess', (x) => { x.network = 'ws'; x.ws.path = '/vm'; tlsFiles(x) })
v3('t-trojan-reality', 35010, 'trojan', reality)
v3('t-trojan-grpc-tls', 35011, 'trojan', (x) => { x.network = 'grpc'; x.grpc.serviceName = 'tg'; tlsFiles(x) })
v3('t-ss2022', 35012, 'shadowsocks', (x) => { x.ss.method = '2022-blake3-aes-256-gcm'; x.ss.password = b64(32) })
v3('t-ss-chacha', 35013, 'shadowsocks', (x) => { x.ss.method = 'chacha20-ietf-poly1305'; x.ss.password = 'b1c2d3e4f5a6b7c8' })
v3('t-hysteria', 35014, 'hysteria', (x) => { tlsFiles(x); x.hy.obfs = '' })
v3('t-tuic', 35015, 'tuic', (x) => { Object.assign(x.tuic, { certificate: tlsCert, private_key: tlsKey }) })
v3('t-mtproto', 35016, 'mtproto', () => {})
v3('t-wireguard', 35017, 'wireguard', (x) => { x.wg.secretKey = b64(32) })
v3('t-masque', 35018, 'masque', tlsFiles)
v3('t-mixed', 35019, 'mixed', (x) => { x.mixed.accounts = 'u1:p1' })
v3('t-tunnel', 35020, 'tunnel', (x) => { x.tunnel.address = '1.1.1.1'; x.tunnel.port = 53; x.tunnel.network = 'udp' })
v3('t-http', 35021, 'http', (x) => { x.http.accounts = 'u1:p1' })

// 3x-ui 2.9, in docker: its certificate pasted, not a file of the host.
const v2 = (n, p, proto, e) => out.push(make('xui-old', n, p, proto, e))
v2('o-vless-reality', 33002, 'vless', reality)
v2('o-vmess-ws', 33003, 'vmess', (x) => { x.network = 'ws'; x.ws.path = '/ov' })
v2('o-trojan-tls', 33004, 'trojan', tlsText)
v2('o-ss2022', 33005, 'shadowsocks', (x) => { x.ss.method = '2022-blake3-aes-128-gcm'; x.ss.password = b64(16) })
v2('o-hysteria', 33006, 'hysteria', tlsText)
v2('o-mixed', 33007, 'mixed', (x) => { x.mixed.accounts = 'u1:p1' })

// Pasarguard: its kinds, the certificate pasted (its node runs in docker).
const pg = (n, p, proto, e) => out.push(make('pasarguard', n, p, proto, e))
pg('P-VLESS-REALITY', 36001, 'vless', reality)
pg('P-VLESS-XHTTP', 36002, 'vless', (x) => { x.network = 'xhttp'; x.xhttp.path = '/px' })
pg('P-VMESS-GRPC', 36003, 'vmess', (x) => { x.network = 'grpc'; x.grpc.serviceName = 'pv' })
pg('P-TROJAN-TLS', 36004, 'trojan', tlsText)
pg('P-SS2022', 36005, 'shadowsocks', (x) => { x.ss.method = '2022-blake3-aes-256-gcm'; x.ss.password = b64(32) })
pg('P-HYSTERIA', 36006, 'hysteria', tlsText)
pg('P-MIXED', 36007, 'mixed', (x) => { x.mixed.accounts = 'u1:p1' })
process.stdout.write(JSON.stringify(out, null, 1))
