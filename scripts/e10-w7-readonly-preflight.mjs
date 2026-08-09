#!/usr/bin/env node
import { createPublicKey, verify } from 'node:crypto'
import { lstat, readFile } from 'node:fs/promises'
import { resolve } from 'node:path'

const rootKeyId = 'stride-e10-w7-root-2026-08'
const productionRootRaw = 'dVFJfc5Kf9e2BnEMi9wxH+ku0kiFOhVWnxCt4oYotHc='
const kinds = ['native_distribution','iphone_physical','ipad_physical','accessibility_privacy','restrictive_turn_webrtc','encrypted_offsite_restore','ha_failover','independent_release_attestation']
const hex64 = /^[a-f0-9]{64}$/; const hex40 = /^[a-f0-9]{40}$/
const exact = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).sort().join('\0') === [...keys].sort().join('\0')
const resultInput = e => Buffer.from(`meetingassist/stride/e10/w7/validator-result/v1\0${e.schema}\0${e.keyId}\0${e.trustPolicyDigest}\0${e.manifestDigest}\0${JSON.stringify(e.payload)}`)
const policyInput = p => Buffer.from(`meetingassist/stride/e10/w7/node-root-policy/v1\0${p.schema}\0${p.rootKeyId}\0${p.resultKeyId}\0${p.resultPublicKey}\0${p.trustPolicyDigest}\0${p.manifestDigest}`)
const rawPublicKey = raw => createPublicKey({key:Buffer.concat([Buffer.from('302a300506032b6570032100','hex'),Buffer.from(raw,'base64')]),format:'der',type:'spki'})

export function validateW7ReadOnlyEnvelope(envelope, policy) {
  if (!exact(policy,['schema','rootKeyId','resultKeyId','resultPublicKey','trustPolicyDigest','manifestDigest','signature']) || policy.schema!=='stride.e10.w7.node-root-policy.v1' || policy.rootKeyId!==rootKeyId || !hex64.test(policy.trustPolicyDigest??'') || !hex64.test(policy.manifestDigest??'')) return {ready:false,reasons:['w7_root_policy_invalid']}
  let rootOK=false; try { rootOK=verify(null,policyInput(policy),rawPublicKey(productionRootRaw),Buffer.from(policy.signature,'base64')) } catch {}
  if(!rootOK) return {ready:false,reasons:['w7_root_policy_signature_invalid']}
  if (!exact(envelope, ['schema','keyId','trustPolicyDigest','manifestDigest','payload','signature']) || envelope.schema !== 'stride.e10.w7.validator-result-envelope.v1' || envelope.keyId !== policy.resultKeyId || envelope.trustPolicyDigest !== policy.trustPolicyDigest || envelope.manifestDigest !== policy.manifestDigest) return {ready:false,reasons:['w7_result_envelope_binding_invalid']}
  let authenticated = false
  try { authenticated = verify(null, resultInput(envelope), policy.resultPublicKey, Buffer.from(envelope.signature, 'base64')) } catch {}
  if (!authenticated) return {ready:false,reasons:['w7_result_envelope_signature_invalid']}
  const p = envelope.payload, keys = ['validator','ready','reasons','candidateCommit','candidateBuild','evidenceKinds','sourceMode']
  if (!exact(p, keys) || p.validator !== 'ValidateStrideE10W7Acceptance' || p.ready !== true || !Array.isArray(p.reasons) || p.reasons.length || !hex40.test(p.candidateCommit ?? '') || !Number.isSafeInteger(p.candidateBuild) || p.candidateBuild < 1 || p.sourceMode !== 'external_signed_receipts' || !Array.isArray(p.evidenceKinds) || p.evidenceKinds.join('\0') !== kinds.join('\0')) return {ready:false,reasons:['w7_authenticated_result_not_ready']}
  return {ready:true,reasons:[],manifestDigest:envelope.manifestDigest}
}

async function safeJSON(path) { if(resolve(path)!==path) throw new Error('path must be absolute'); const s=await lstat(path); if(!s.isFile()||s.isSymbolicLink()||s.size>65536) throw new Error('unsafe file'); return JSON.parse(await readFile(path,'utf8')) }
async function main(argv) { if(argv.length!==4||argv[0]!=='--policy'||argv[2]!=='--result') throw new Error('usage: --policy <absolute-json-path> --result <absolute-json-path>'); const result=validateW7ReadOnlyEnvelope(await safeJSON(argv[3]),await safeJSON(argv[1])); console.log(JSON.stringify(result)); if(!result.ready)process.exitCode=1 }
if (process.argv[1] && resolve(process.argv[1]) === resolve(new URL(import.meta.url).pathname)) main(process.argv.slice(2)).catch(e=>{console.error(JSON.stringify({ready:false,reasons:[String(e.message||e)]}));process.exitCode=1})
