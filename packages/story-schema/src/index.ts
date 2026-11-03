import { z } from 'zod'

export const LocaleCodeSchema = z.string().min(2)

export const LocalizedTextSchema = z.record(LocaleCodeSchema, z.string().min(1))

export const VariableValueSchema = z.union([z.string(), z.number(), z.boolean()])
export type VariableValue = z.infer<typeof VariableValueSchema>

export const VariableMapSchema = z.record(z.string(), VariableValueSchema)
export type VariableMap = z.infer<typeof VariableMapSchema>

export const EffectMapSchema = z.record(z.string(), VariableValueSchema)
export type EffectMap = z.infer<typeof EffectMapSchema>

export const ProjectManifestSchema = z
  .object({
    projectId: z.string().min(1),
    title: LocalizedTextSchema,
    description: LocalizedTextSchema.optional(),
    version: z.string().min(1),
    engineVersion: z.string().min(1),
    targetLanguage: LocaleCodeSchema,
    nativeLanguage: LocaleCodeSchema,
    level: z.enum(['A1', 'A2', 'B1', 'B2', 'C1', 'C2']),
    contentRating: z.enum(['everyone', 'teen', 'mature']).default('teen'),
    createdAt: z.string().datetime(),
    updatedAt: z.string().datetime()
  })
  .strict()
export type ProjectManifest = z.infer<typeof ProjectManifestSchema>

export const ProjectSchema = z
  .object({
    id: z.string().min(1),
    title: LocalizedTextSchema,
    description: LocalizedTextSchema.optional(),
    targetLanguage: LocaleCodeSchema,
    nativeLanguage: LocaleCodeSchema,
    level: z.enum(['A1', 'A2', 'B1', 'B2', 'C1']),
    status: z.enum(['draft', 'ready', 'published']).default('draft'),
    createdAt: z.string().datetime(),
    updatedAt: z.string().datetime()
  })
  .strict()
export type Project = z.infer<typeof ProjectSchema>

export const StoryEpisodeRefSchema = z
  .object({
    id: z.string().min(1),
    titleKey: z.string().min(1),
    sceneIds: z.array(z.string().min(1)).min(1)
  })
  .strict()
export type StoryEpisodeRef = z.infer<typeof StoryEpisodeRefSchema>

export const StoryDefinitionSchema = z
  .object({
    storyId: z.string().min(1),
    startEpisodeId: z.string().min(1),
    characters: z.array(z.string().min(1)).default([]),
    variables: VariableMapSchema.default({}),
    episodes: z.array(StoryEpisodeRefSchema).min(1)
  })
  .strict()
export type StoryDefinition = z.infer<typeof StoryDefinitionSchema>

export const BaseNodeSchema = z
  .object({
    id: z.string().min(1)
  })
  .strict()

export const DialogueNodeSchema = BaseNodeSchema.extend({
  type: z.literal('dialogue'),
  speaker: z.string().min(1),
  textKey: z.string().min(1),
  emotion: z.string().min(1).optional(),
  sprite: z.string().min(1).optional(),
  voice: z.string().min(1).optional(),
  learningPointIds: z.array(z.string().min(1)).default([]),
  next: z.string().min(1).optional()
}).strict()
export type DialogueNode = z.infer<typeof DialogueNodeSchema>

export const NarrationNodeSchema = BaseNodeSchema.extend({
  type: z.literal('narration'),
  textKey: z.string().min(1),
  voice: z.string().min(1).optional(),
  learningPointIds: z.array(z.string().min(1)).default([]),
  next: z.string().min(1).optional()
}).strict()
export type NarrationNode = z.infer<typeof NarrationNodeSchema>

export const ChoiceOptionSchema = z
  .object({
    id: z.string().min(1),
    textKey: z.string().min(1),
    level: z.enum(['easy', 'natural', 'expressive']).optional(),
    next: z.string().min(1),
    effects: EffectMapSchema.default({})
  })
  .strict()
export type ChoiceOption = z.infer<typeof ChoiceOptionSchema>

export const ChoiceNodeSchema = BaseNodeSchema.extend({
  type: z.literal('choice'),
  promptKey: z.string().min(1),
  choices: z.array(ChoiceOptionSchema).min(1)
}).strict()
export type ChoiceNode = z.infer<typeof ChoiceNodeSchema>

export const SetVariableNodeSchema = BaseNodeSchema.extend({
  type: z.literal('setVariable'),
  effects: EffectMapSchema,
  next: z.string().min(1)
}).strict()
export type SetVariableNode = z.infer<typeof SetVariableNodeSchema>

export const ComparisonOperatorSchema = z.enum(['eq', 'neq', 'gt', 'gte', 'lt', 'lte'])
export type ComparisonOperator = z.infer<typeof ComparisonOperatorSchema>

export const ConditionBranchSchema = z
  .object({
    variable: z.string().min(1),
    operator: ComparisonOperatorSchema,
    value: VariableValueSchema,
    next: z.string().min(1)
  })
  .strict()
export type ConditionBranch = z.infer<typeof ConditionBranchSchema>

export const ConditionalNodeSchema = BaseNodeSchema.extend({
  type: z.literal('conditional'),
  conditions: z.array(ConditionBranchSchema).min(1),
  defaultNext: z.string().min(1)
}).strict()
export type ConditionalNode = z.infer<typeof ConditionalNodeSchema>

export const JumpNodeSchema = BaseNodeSchema.extend({
  type: z.literal('jump'),
  next: z.string().min(1)
}).strict()
export type JumpNode = z.infer<typeof JumpNodeSchema>

export const EndNodeSchema = BaseNodeSchema.extend({
  type: z.literal('end'),
  endingKey: z.string().min(1).optional()
}).strict()
export type EndNode = z.infer<typeof EndNodeSchema>

export const StoryNodeSchema = z.discriminatedUnion('type', [
  DialogueNodeSchema,
  NarrationNodeSchema,
  ChoiceNodeSchema,
  SetVariableNodeSchema,
  ConditionalNodeSchema,
  JumpNodeSchema,
  EndNodeSchema
])
export type StoryNode = z.infer<typeof StoryNodeSchema>

export const SceneDefinitionSchema = z
  .object({
    id: z.string().min(1),
    background: z.string().min(1).optional(),
    bgm: z.string().min(1).optional(),
    startNodeId: z.string().min(1),
    nodes: z.array(StoryNodeSchema).min(1)
  })
  .strict()
export type SceneDefinition = z.infer<typeof SceneDefinitionSchema>

export const EpisodeDefinitionSchema = z
  .object({
    episodeId: z.string().min(1),
    titleKey: z.string().min(1),
    summaryKey: z.string().min(1).optional(),
    durationMinutes: z.number().int().positive().optional(),
    storyGoal: z.string().min(1).optional(),
    learningGoal: z.string().min(1).optional(),
    targetExpressionIds: z.array(z.string().min(1)).default([]),
    cliffhangerKey: z.string().min(1).optional(),
    scenes: z.array(SceneDefinitionSchema).min(1)
  })
  .strict()
export type EpisodeDefinition = z.infer<typeof EpisodeDefinitionSchema>

export const AssetRefSchema = z
  .object({
    id: z.string().min(1),
    key: z.string().min(1).optional(),
    url: z.string().url().optional()
  })
  .strict()
export type AssetRef = z.infer<typeof AssetRefSchema>

export const CharacterDefinitionSchema = z
  .object({
    id: z.string().min(1),
    name: z.string().min(1),
    role: z.string().min(1),
    personality: z.array(z.string().min(1)).default([]),
    speakingStyle: z.record(LocaleCodeSchema, z.string().min(1)).default({}),
    relationshipArc: z
      .array(
        z
          .object({
            episodeId: z.string().min(1),
            state: z.string().min(1)
          })
          .strict()
      )
      .default([]),
    visualDesign: z
      .object({
        ageRange: z.string().min(1).optional(),
        hair: z.string().min(1).optional(),
        eyes: z.string().min(1).optional(),
        outfit: z.string().min(1).optional(),
        colorPalette: z.array(z.string().min(1)).optional(),
        artStyle: z.string().min(1).optional()
      })
      .strict()
      .default({}),
    sprites: z.record(z.string(), AssetRefSchema).default({}),
    voice: z
      .object({
        provider: z.literal('openai'),
        voice: z.string().min(1),
        instructions: z.string().min(1)
      })
      .strict()
      .optional()
  })
  .strict()
export type CharacterDefinition = z.infer<typeof CharacterDefinitionSchema>

export const LocaleBundleSchema = z
  .object({
    locale: LocaleCodeSchema,
    entries: z.record(z.string().min(1), z.string())
  })
  .strict()
export type LocaleBundle = z.infer<typeof LocaleBundleSchema>

export const LearningTokenSchema = z
  .object({
    text: z.string().min(1),
    lemma: z.string().min(1).optional(),
    meaning: z.string().min(1).optional()
  })
  .strict()
export type LearningToken = z.infer<typeof LearningTokenSchema>

export const LearningExampleSchema = z
  .object({
    text: z.string().min(1),
    translation: z.string().min(1)
  })
  .strict()
export type LearningExample = z.infer<typeof LearningExampleSchema>

export const LearningItemSchema = z
  .object({
    id: z.string().min(1),
    type: z.enum(['word', 'phrase', 'expression', 'pattern']),
    targetLanguage: LocaleCodeSchema,
    text: z.string().min(1),
    translation: z.string().min(1).optional(),
    level: z.enum(['A1', 'A2', 'B1', 'B2', 'C1', 'C2']).optional(),
    usage: z.string().min(1).optional(),
    tokens: z.array(LearningTokenSchema).default([]),
    examples: z.array(LearningExampleSchema).default([]),
    review: z
      .object({
        firstSeen: z.string().min(1).optional(),
        reuseIn: z.array(z.string().min(1)).default([])
      })
      .strict()
      .optional()
  })
  .strict()
export type LearningItem = z.infer<typeof LearningItemSchema>

export const LearningBundleSchema = z
  .object({
    targetLanguage: LocaleCodeSchema,
    items: z.array(LearningItemSchema)
  })
  .strict()
export type LearningBundle = z.infer<typeof LearningBundleSchema>
