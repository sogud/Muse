import type {
  CharacterDefinition,
  EpisodeDefinition,
  LearningBundle,
  LearningItem,
  LocaleBundle,
  ProjectManifest,
  StoryDefinition
} from '@aivn/story-schema'
import { hasValidationErrors, validateStoryContent, type ValidationDiagnostic } from '@aivn/validator'

export type StoryPackAsset = {
  assetKey: string
  assetType: string
  storageKey?: string
  url?: string
  prompt?: string
  metadata?: unknown
}

export type StoryPackManifest = {
  storyId: string
  version: string
  engineVersion: string
  title: Record<string, string>
  targetLanguage: string
  nativeLanguage: string
  level: string
  entry: {
    episodeId: string
    sceneId: string
    nodeId: string
  }
  assetIndex: 'assets.json'
}

export type StoryPack = {
  manifest: StoryPackManifest
  story: StoryDefinition
  characters: CharacterDefinition[]
  episodes: EpisodeDefinition[]
  locales: LocaleBundle[]
  learning: LearningBundle[]
  assets: StoryPackAsset[]
  checksums: Record<string, string>
}

export type BuildStoryPackInput = {
  project: ProjectManifest
  story: StoryDefinition
  characters: CharacterDefinition[]
  episodes: EpisodeDefinition[]
  locales: LocaleBundle[]
  learning: LearningBundle[]
  assets: StoryPackAsset[]
}

export type BuildStoryPackResult = {
  storypack: StoryPack
  diagnostics: ValidationDiagnostic[]
}

export class StoryPackBuildError extends Error {
  diagnostics: ValidationDiagnostic[]

  constructor(diagnostics: ValidationDiagnostic[]) {
    super('StoryPack validation failed')
    this.name = 'StoryPackBuildError'
    this.diagnostics = diagnostics
  }
}

export const buildStoryPack = (input: BuildStoryPackInput): BuildStoryPackResult => {
  const diagnostics = validateForStoryPack(input)

  if (hasValidationErrors(diagnostics)) {
    throw new StoryPackBuildError(diagnostics)
  }

  const storypack: StoryPack = {
    manifest: createManifest(input),
    story: input.story,
    characters: input.characters,
    episodes: input.episodes,
    locales: input.locales,
    learning: input.learning,
    assets: input.assets,
    checksums: createChecksums(input)
  }

  return {
    storypack,
    diagnostics
  }
}

export const validateForStoryPack = (input: BuildStoryPackInput): ValidationDiagnostic[] => {
  const diagnostics: ValidationDiagnostic[] = []
  const targetLocale = input.locales.find((locale) => locale.locale === input.project.targetLanguage)
  const nativeLocale = input.locales.find((locale) => locale.locale === input.project.nativeLanguage)

  if (!targetLocale) {
    diagnostics.push({
      severity: 'error',
      code: 'MISSING_TARGET_LOCALE',
      message: `Missing target locale "${input.project.targetLanguage}".`,
      path: 'locales'
    })
  }

  if (!nativeLocale) {
    diagnostics.push({
      severity: 'error',
      code: 'MISSING_NATIVE_LOCALE',
      message: `Missing native locale "${input.project.nativeLanguage}".`,
      path: 'locales'
    })
  }

  if (!targetLocale) {
    return diagnostics
  }

  return [
    ...diagnostics,
    ...validateStoryContent({
      story: input.story,
      episodes: input.episodes,
      targetLocale,
      nativeLocale,
      learningItems: collectLearningItems(input.learning),
      assets: input.assets.map((asset) => ({
        assetKey: asset.assetKey,
        storageKey: asset.storageKey,
        id: asset.assetKey
      }))
    })
  ]
}

const createManifest = (input: BuildStoryPackInput): StoryPackManifest => {
  const episode = input.episodes.find((candidate) => candidate.episodeId === input.story.startEpisodeId)
  const storyEpisodeRef = input.story.episodes.find((candidate) => candidate.id === input.story.startEpisodeId)
  const sceneId = storyEpisodeRef?.sceneIds[0] ?? episode?.scenes[0]?.id
  const scene = episode?.scenes.find((candidate) => candidate.id === sceneId) ?? episode?.scenes[0]

  if (!episode || !scene) {
    throw new StoryPackBuildError([
      {
        severity: 'error',
        code: 'MISSING_ENTRY',
        message: 'StoryPack entry episode or scene is missing.',
        path: 'story.startEpisodeId'
      }
    ])
  }

  return {
    storyId: input.story.storyId,
    version: input.project.version,
    engineVersion: input.project.engineVersion,
    title: input.project.title,
    targetLanguage: input.project.targetLanguage,
    nativeLanguage: input.project.nativeLanguage,
    level: input.project.level,
    entry: {
      episodeId: episode.episodeId,
      sceneId: scene.id,
      nodeId: scene.startNodeId
    },
    assetIndex: 'assets.json'
  }
}

const createChecksums = (input: BuildStoryPackInput): Record<string, string> => ({
  'manifest.json': hashJson({
    storyId: input.story.storyId,
    version: input.project.version,
    engineVersion: input.project.engineVersion
  }),
  'story.json': hashJson(input.story),
  'characters.json': hashJson(input.characters),
  'episodes.json': hashJson(input.episodes),
  'locales.json': hashJson(input.locales),
  'learning.json': hashJson(input.learning),
  'assets.json': hashJson(input.assets)
})

const collectLearningItems = (learningBundles: LearningBundle[]): LearningItem[] =>
  learningBundles.flatMap((bundle) => bundle.items)

const hashJson = (value: unknown): string => {
  const text = stableStringify(value)
  let hash = 2166136261

  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }

  return (hash >>> 0).toString(16).padStart(8, '0')
}

const stableStringify = (value: unknown): string => {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value)
  }

  if (Array.isArray(value)) {
    return `[${value.map((item) => stableStringify(item)).join(',')}]`
  }

  const record = value as Record<string, unknown>
  return `{${Object.keys(record)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${stableStringify(record[key])}`)
    .join(',')}}`
}
