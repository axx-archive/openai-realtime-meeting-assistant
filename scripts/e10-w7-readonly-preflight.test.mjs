import assert from 'node:assert/strict'
import test from 'node:test'
import { generateKeyPairSync, sign } from 'node:crypto'
import { validateW7ReadOnlyEnvelope } from './e10-w7-readonly-preflight.mjs'

const kinds=['native_distribution','iphone_physical','ipad_physical','accessibility_privacy','restrictive_turn_webrtc','encrypted_offsite_restore','ha_failover','independent_release_attestation']
const resultPrivateKey=`-----BEGIN PRIVATE KEY-----
MC4CAQAwBQYDK2VwBCIEIInF0OXAj+urYz7F0yXIlMEePXI3XEDLtTLnXbeeICQ2
-----END PRIVATE KEY-----
`
const sealedPolicy={schema:'stride.e10.w7.node-root-policy.v1',rootKeyId:'stride-e10-w7-root-2026-08',resultKeyId:'pinned-w7-result',resultPublicKey:`-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEApA/CF2ABaAeVklVP4CDIe/hoUnzp5MJhTBRBEdoHwP0=
-----END PUBLIC KEY-----
`,trustPolicyDigest:'a'.repeat(64),manifestDigest:'b'.repeat(64),signature:'wd2UXhNX/E9gxfELL+Fz2oTLiZFHsXWcP68yUh3xhlaGgUXWMzyVBz1IUUw4QfhC4qchb47SGgwnycquwu8QAQ=='}
function envelope(policy=sealedPolicy,key=resultPrivateKey){const e={schema:'stride.e10.w7.validator-result-envelope.v1',keyId:policy.resultKeyId,trustPolicyDigest:policy.trustPolicyDigest,manifestDigest:policy.manifestDigest,payload:{validator:'ValidateStrideE10W7Acceptance',ready:true,reasons:[],candidateCommit:'c'.repeat(40),candidateBuild:50,evidenceKinds:kinds,sourceMode:'external_signed_receipts'},signature:''};const input=Buffer.from(`meetingassist/stride/e10/w7/validator-result/v1\0${e.schema}\0${e.keyId}\0${e.trustPolicyDigest}\0${e.manifestDigest}\0${JSON.stringify(e.payload)}`);e.signature=sign(null,input,key).toString('base64');return e}
test('W7 adapter requires the presealed immutable root policy',()=>{assert.equal(validateW7ReadOnlyEnvelope(envelope(),sealedPolicy).ready,true);const attackerRoot=generateKeyPairSync('ed25519'),attackerResult=generateKeyPairSync('ed25519'),replaced={...sealedPolicy,resultPublicKey:attackerResult.publicKey.export({type:'spki',format:'pem'}),signature:''};const policyInput=Buffer.from(`meetingassist/stride/e10/w7/node-root-policy/v1\0${replaced.schema}\0${replaced.rootKeyId}\0${replaced.resultKeyId}\0${replaced.resultPublicKey}\0${replaced.trustPolicyDigest}\0${replaced.manifestDigest}`);replaced.signature=sign(null,policyInput,attackerRoot.privateKey).toString('base64');assert.equal(validateW7ReadOnlyEnvelope(envelope(replaced,attackerResult.privateKey),replaced).ready,false);const synthetic=envelope();synthetic.payload.sourceMode='synthetic';assert.equal(validateW7ReadOnlyEnvelope(synthetic,sealedPolicy).ready,false)})
