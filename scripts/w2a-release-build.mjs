#!/usr/bin/env node

// Build/verify the W2A release image from a read-only git export. The signed
// manifest is external to the image to avoid a circular OCI digest, but binds
// the commit/tree, every tracked input, build config, executable bytes, and
// independently re-read registry manifest digest.

import { execFile, spawn } from 'node:child_process'
import { createHash, createPrivateKey, createPublicKey, sign, verify } from 'node:crypto'
import { mkdtemp, open, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { promisify } from 'node:util'

const execFileAsync = promisify(execFile)
const root = process.cwd()
const options = parseArgs(process.argv.slice(2))

try {
  if (options.verify) await verifyDeployment()
  else await buildRelease()
} catch (error) {
  process.stderr.write(`w2a-release-build: ${error?.message || error}\n`)
  process.exitCode = 1
}

async function buildRelease() {
  required('--image-ref', options.imageRef)
  required('--private-key', options.privateKey)
  required('--output', options.output)
  const [{ stdout: commit }, { stdout: dirty }, { stdout: tree }, { stdout: transitive }, { stdout: archive }] = await Promise.all([
    git(['rev-parse', 'HEAD']), git(['status', '--porcelain', '--untracked-files=all']), git(['rev-parse', 'HEAD^{tree}']),
    git(['ls-tree', '-r', '--full-tree', 'HEAD'], { maxBuffer: 64 << 20 }),
    git(['archive', '--format=tar', 'HEAD'], { encoding: 'buffer', maxBuffer: 512 << 20 })
  ])
  if (String(dirty).trim()) throw new Error('release checkout is not clean')
  const releaseCommit = String(commit).trim()
  const gitTreeObject = String(tree).trim()
  if (!/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(releaseCommit) || !/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(gitTreeObject)) throw new Error('release git identity is invalid')
  const inputLines = String(transitive).trimEnd().split('\n').filter(Boolean)
  const configPaths = new Set(['Dockerfile', 'go.mod', 'go.sum', 'deploy/digitalocean/docker-compose.yml', 'deploy/digitalocean/Caddyfile'])
  const configLines = inputLines.filter(line => configPaths.has(line.split('\t', 2)[1]))
  if (!inputLines.length || configLines.length !== configPaths.size) throw new Error('transitive/config input inventory is incomplete')
  const gitTreeDigest = sha256(gitTreeObject)
  const sourceArchiveSha256 = sha256(archive)
  const transitiveInputsSha256 = sha256(String(transitive))
  const configSha256 = sha256(`${configLines.join('\n')}\n`)

  const temporary = await mkdtemp(`${tmpdir()}/bonfire-w2a-release-`)
  const exportRoot = resolve(temporary, 'source')
  const metadataPath = resolve(temporary, 'build-metadata.json')
  const binaryPath = resolve(temporary, 'meetingassist')
  let container = ''
  try {
    await execFileAsync('mkdir', ['-p', exportRoot])
    await spawnWithInput('tar', ['-xf', '-', '-C', exportRoot], archive)
    await execFileAsync('chmod', ['-R', 'a-w', exportRoot])
    await execFileAsync('docker', [
      'buildx', 'build', '--push', '--metadata-file', metadataPath, '--tag', options.imageRef,
      '--build-arg', `BONFIRE_RELEASE_COMMIT=${releaseCommit}`,
      '--build-arg', `BONFIRE_GIT_TREE_DIGEST=${gitTreeDigest}`,
      '--build-arg', `BONFIRE_BUILD_CONFIG_SHA256=${configSha256}`,
      '--build-arg', `BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256=${transitiveInputsSha256}`,
      '--build-arg', `BONFIRE_SOURCE_ARCHIVE_SHA256=${sourceArchiveSha256}`,
      exportRoot
    ], { maxBuffer: 64 << 20 })
    const metadata = JSON.parse(await readFile(metadataPath, 'utf8'))
    const ociImageDigest = String(metadata['containerimage.digest'] || metadata['containerimage.descriptor']?.digest || '')
    if (!/^sha256:[0-9a-f]{64}$/.test(ociImageDigest)) throw new Error('buildx did not return an OCI digest')
    const immutableImage = `${options.imageRef.split('@')[0]}@${ociImageDigest}`
    const { stdout: registryRaw } = await execFileAsync('docker', ['buildx', 'imagetools', 'inspect', immutableImage, '--raw'], { encoding: 'buffer', maxBuffer: 64 << 20 })
    if (`sha256:${sha256(registryRaw)}` !== ociImageDigest) throw new Error('registry OCI manifest differs from buildx result')
	await execFileAsync('docker', ['pull', immutableImage], { maxBuffer: 64 << 20 })
    ;({ stdout: container } = await execFileAsync('docker', ['create', immutableImage]))
    container = String(container).trim()
    await execFileAsync('docker', ['cp', `${container}:/app/meetingassist`, binaryPath])
    const binarySha256 = sha256(await readFile(binaryPath))
    const payload = {
      schema: 'bonfire.w2a.build-manifest.v1', releaseCommit, gitTreeObject, gitTreeDigest,
      sourceArchiveSha256, transitiveInputsSha256, configSha256, inputCount: inputLines.length,
      binarySha256, ociImageDigest, imageReference: immutableImage, createdAt: new Date().toISOString()
    }
	const privateKey = parseEd25519PrivateKey(await readFile(resolve(options.privateKey)))
    if (privateKey.asymmetricKeyType !== 'ed25519') throw new Error('build manifest private key is not Ed25519')
    const signature = sign(null, Buffer.from(stableStringify(payload)), privateKey).toString('base64')
    const body = Buffer.from(`${JSON.stringify({
      schema: 'bonfire.w2a.signed-build-manifest.v1', payload,
      signature: { algorithm: 'ed25519', keyId: 'bonfire-release-operator', value: signature }
    })}\n`)
    await writeExclusive(resolve(options.output), body)
    process.stdout.write(`${JSON.stringify({
      manifest: resolve(options.output), manifestSha256: sha256(body), releaseCommit, immutableImage,
      runtimeEnvironment: {
        BONFIRE_RELEASE_COMMIT: releaseCommit, BONFIRE_GIT_TREE_DIGEST: gitTreeDigest,
        BONFIRE_BUILD_MANIFEST_SHA256: sha256(body), BONFIRE_BUILD_CONFIG_SHA256: configSha256,
        BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256: transitiveInputsSha256, BONFIRE_SOURCE_ARCHIVE_SHA256: sourceArchiveSha256,
        BONFIRE_IMAGE_DIGEST: ociImageDigest.replace(/^sha256:/, '')
      }
    }, null, 2)}\n`)
  } finally {
    if (container) await execFileAsync('docker', ['rm', '-f', container]).catch(() => {})
    await rm(temporary, { recursive: true, force: true })
  }
}

async function verifyDeployment() {
  required('--manifest', options.manifest)
  required('--public-key', options.publicKey)
  required('--container', options.container)
  const raw = await readFile(resolve(options.manifest))
  const signed = JSON.parse(raw)
	const publicKey = parseEd25519PublicKey(await readFile(resolve(options.publicKey)))
  const signature = Buffer.from(String(signed?.signature?.value || ''), 'base64')
  if (signed?.schema !== 'bonfire.w2a.signed-build-manifest.v1' || signed?.payload?.schema !== 'bonfire.w2a.build-manifest.v1'
      || signed?.signature?.algorithm !== 'ed25519' || signed?.signature?.keyId !== 'bonfire-release-operator'
      || publicKey.asymmetricKeyType !== 'ed25519' || signature.length !== 64
      || !verify(null, Buffer.from(stableStringify(signed.payload)), publicKey, signature)) throw new Error('signed build manifest verification failed')
  const payload = signed.payload
  const [{ stdout: configuredImage }, { stdout: runtimeBinary }, { stdout: labels }] = await Promise.all([
    execFileAsync('docker', ['inspect', '--format', '{{.Config.Image}}', options.container]),
    execFileAsync('docker', ['exec', options.container, 'sha256sum', '/app/meetingassist']),
    execFileAsync('docker', ['inspect', '--format', '{{json .Config.Labels}}', options.container])
  ])
  if (String(configuredImage).trim() !== payload.imageReference) throw new Error('deployed container is not configured from the immutable manifest image')
  if (String(runtimeBinary).trim().split(/\s+/, 1)[0] !== payload.binarySha256.replace(/^sha256:/, '')) throw new Error('deployed executable differs from signed build manifest')
  const runtimeLabels = JSON.parse(labels)
  if (runtimeLabels['org.opencontainers.image.revision'] !== payload.releaseCommit
      || runtimeLabels['xyz.thebonfire.git-tree-digest'] !== payload.gitTreeDigest.replace(/^sha256:/, '')
      || runtimeLabels['xyz.thebonfire.config-digest'] !== payload.configSha256.replace(/^sha256:/, '')
      || runtimeLabels['xyz.thebonfire.transitive-inputs-digest'] !== payload.transitiveInputsSha256.replace(/^sha256:/, '')
      || runtimeLabels['xyz.thebonfire.source-archive-digest'] !== payload.sourceArchiveSha256.replace(/^sha256:/, '')) throw new Error('deployed image labels differ from signed release inputs')
  const { stdout: registryRaw } = await execFileAsync('docker', ['buildx', 'imagetools', 'inspect', payload.imageReference, '--raw'], { encoding: 'buffer', maxBuffer: 64 << 20 })
  if (`sha256:${sha256(registryRaw)}` !== payload.ociImageDigest) throw new Error('registry OCI identity differs from signed manifest')
  process.stdout.write(`${JSON.stringify({ verified: true, manifestSha256: sha256(raw), releaseCommit: payload.releaseCommit, ociImageDigest: payload.ociImageDigest, binarySha256: payload.binarySha256 })}\n`)
}

function parseArgs(args) {
  const parsed = {}
  for (let index = 0; index < args.length; index++) {
    const value = args[index + 1]
    if (args[index] === '--verify') parsed.verify = true
    else if (args[index] === '--image-ref' && value) parsed.imageRef = args[++index]
	else if (args[index] === '--private-key' && value) { parsed.privateKey = value; index++ }
	else if (args[index] === '--output' && value) { parsed.output = value; index++ }
	else if (args[index] === '--manifest' && value) { parsed.manifest = value; index++ }
	else if (args[index] === '--public-key' && value) { parsed.publicKey = value; index++ }
	else if (args[index] === '--container' && value) { parsed.container = value; index++ }
  }
  return parsed
}

function required(name, value) { if (!String(value || '').trim()) throw new Error(`${name} is required`) }
function git(args, options = {}) { return execFileAsync('git', args, { cwd: root, timeout: 60_000, ...options }) }
function sha256(value) { return createHash('sha256').update(value).digest('hex') }
function stableStringify(value) {
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`
  if (value && typeof value === 'object') return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${stableStringify(value[key])}`).join(',')}}`
  return JSON.stringify(value)
}
function parseEd25519PublicKey(raw) {
  try { return createPublicKey(raw) } catch {}
  const decoded = Buffer.from(String(raw).trim(), 'base64')
  if (decoded.length !== 32) throw new Error('build manifest public key is not Ed25519')
  return createPublicKey({ key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), decoded]), format: 'der', type: 'spki' })
}
function parseEd25519PrivateKey(raw) {
  try { return createPrivateKey(raw) } catch {}
  const decoded = Buffer.from(String(raw).trim(), 'base64')
  if (decoded.length !== 64) throw new Error('build manifest private key is not Ed25519')
  return createPrivateKey({ key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), decoded.subarray(0, 32)]), format: 'der', type: 'pkcs8' })
}
async function writeExclusive(path, body) {
  await execFileAsync('mkdir', ['-p', dirname(path)])
  const file = await open(path, 'wx', 0o600)
  try { await file.writeFile(body); await file.sync() } finally { await file.close() }
}
async function spawnWithInput(command, args, input) {
  const child = spawn(command, args, { stdio: ['pipe', 'pipe', 'pipe'] })
  const stdout = [], stderr = []
  child.stdout.on('data', chunk => stdout.push(chunk)); child.stderr.on('data', chunk => stderr.push(chunk))
  child.stdin.end(input)
  const code = await new Promise((resolveCode, reject) => { child.once('error', reject); child.once('close', resolveCode) })
  if (code !== 0) throw new Error(`${command} failed (${code}): ${Buffer.concat(stderr).toString('utf8').slice(0, 1000)}`)
  return Buffer.concat(stdout)
}
