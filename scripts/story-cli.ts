/**
 * Thin TypeScript adapter for the AIVN story runtime imported into this repo.
 * The headless Go CLI runs this only for story commands; MyGo and music commands
 * do not require Bun or Node.
 */
import { readFile, writeFile } from 'node:fs/promises'
import { z } from 'zod/v4'
import {
  ProjectManifestSchema,
  StoryDefinitionSchema,
  CharacterDefinitionSchema,
  EpisodeDefinitionSchema,
  LocaleBundleSchema,
  LearningBundleSchema
} from '@aivn/story-schema'
import { buildStoryPack, validateForStoryPack } from '@aivn/storypack'
import type { BuildStoryPackInput } from '@aivn/storypack'

const AssetSchema = z.object({
  assetKey: z.string().min(1),
  assetType: z.string().min(1),
  storageKey: z.string().optional(),
  url: z.url().optional(),
  prompt: z.string().optional(),
  metadata: z.unknown().optional()
}).strict()

const InputSchema = z.object({
  project: ProjectManifestSchema,
  story: StoryDefinitionSchema,
  characters: z.array(CharacterDefinitionSchema).default([]),
  episodes: z.array(EpisodeDefinitionSchema),
  locales: z.array(LocaleBundleSchema),
  learning: z.array(LearningBundleSchema).default([]),
  assets: z.array(AssetSchema).default([])
}).strict()

function readFlag(args: string[], flag: string) {
  const idx = args.indexOf(flag)
  if (idx < 0) return ''
  if (idx === args.length - 1 || args[idx + 1].startsWith('--')) {
    throw new Error(`Missing value for ${flag}`)
  }
  return args[idx + 1]
}

async function main() {
  const [op, ...args] = process.argv.slice(2)
  if (op !== 'validate' && op !== 'build') {
    throw new Error('Expected validate or build')
  }
  const inputFile = readFlag(args, '--input')
  if (!inputFile) throw new Error('--input is required')
  const raw = await readFile(inputFile, 'utf8')
  if (raw.length > 10 * 1024 * 1024) throw new Error('Story input exceeds 10 MiB')
  const input = InputSchema.parse(JSON.parse(raw)) as BuildStoryPackInput
  const diagnostics = validateForStoryPack(input)
  if (op === 'validate') {
    const result = {
      valid: !diagnostics.some((d) => d.severity === 'error'),
      diagnostics
    }
    process.stdout.write(JSON.stringify(result) + '\n')
    return
  }
  if (diagnostics.some((d) => d.severity === 'error')) {
    // Do not write a partial pack when validation failed.
    throw new Error('Story validation failed: ' + diagnostics.filter((d) => d.severity === 'error').map((d) => d.code).join(', '))
  }
  const outputFile = readFlag(args, '--output')
  if (!outputFile) throw new Error('--output is required for story build')
  const result = buildStoryPack(input)
  // No overwrite: the caller selects a new artifact location explicitly.
  await writeFile(outputFile, JSON.stringify(result.storypack, null, 2) + '\n', { flag: 'wx', mode: 0o600 })
  process.stdout.write(JSON.stringify({
    valid: true,
    outputFile,
    storyId: result.storypack.manifest.storyId,
    version: result.storypack.manifest.version,
    episodes: result.storypack.episodes.length,
    assets: result.storypack.assets.length,
    diagnostics: result.diagnostics
  }) + '\n')
}

main().catch((error: unknown) => {
  // Avoid leaking raw input or credentials in failure output.
  const message = error instanceof Error && error.name === 'ZodError'
    ? 'Story input failed schema validation'
    : error instanceof Error ? error.message : 'Story tool failed'
  process.stderr.write(JSON.stringify({ code: 'STORY_ERROR', message }) + '\n')
  process.exitCode = 1
})
