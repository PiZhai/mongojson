import http from 'node:http'
import { parseMongoScript } from './parser.js'
import { matchDocuments, simulateUpdate } from './simulator.js'

const port = Number.parseInt(process.env.PORT ?? '8090', 10)
const host = process.env.HOST ?? '0.0.0.0'
const maxBodyBytes = 4 << 20

function send(response, status, payload) {
  response.writeHead(status, { 'content-type': 'application/json; charset=utf-8' })
  response.end(JSON.stringify(payload))
}

async function readJSON(request) {
  const chunks = []
  let size = 0
  for await (const chunk of request) {
    size += chunk.length
    if (size > maxBodyBytes) throw new Error('request body exceeds 4 MiB')
    chunks.push(chunk)
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}')
}

const server = http.createServer(async (request, response) => {
  try {
    if (request.method === 'GET' && request.url === '/healthz') {
      send(response, 200, { status: 'ok' })
      return
    }
    if (request.method !== 'POST') {
      send(response, 404, { error: 'not found' })
      return
    }
    const body = await readJSON(request)
    if (request.url === '/v1/parse') {
      send(response, 200, parseMongoScript(body.source))
      return
    }
    if (request.url === '/v1/match') {
      send(response, 200, matchDocuments(body))
      return
    }
    if (request.url === '/v1/simulate-update') {
      send(response, 200, simulateUpdate(body))
      return
    }
    send(response, 404, { error: 'not found' })
  } catch (error) {
    send(response, 400, { error: error instanceof Error ? error.message : 'invalid request' })
  }
})

server.listen(port, host, () => {
  console.log(`mongo script analyzer listening on ${host}:${port}`)
})
