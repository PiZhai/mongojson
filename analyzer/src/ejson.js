import { EJSON } from 'bson'

export function fromExtendedJSON(value) {
  return EJSON.parse(JSON.stringify(value), { relaxed: false })
}

export function toExtendedJSON(value) {
  return JSON.parse(EJSON.stringify(value, null, 0, { relaxed: false }))
}

export function cloneBSON(value) {
  return fromExtendedJSON(toExtendedJSON(value))
}
